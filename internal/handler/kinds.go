package handler

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vsriram/simple-host/internal/app/legacy"
	"github.com/vsriram/simple-host/internal/model"
	"github.com/vsriram/simple-host/internal/workers"
)

// Saved data, step 2: kinds (saved-data redesign, owner approved 2026-09-27;
// INTENT). The owner's agent declares each data name once as one kind, and
// the kind decides who reads and who changes what:
//
//   - Page info (kind "content"): one JSON document. Anyone who can open the
//     site reads it; only the owner writes it (key, connector, or the owner
//     signed in on the site). PUT /v1/sites/{s}/data/{name}.
//   - Submissions (kind "entries"): things visitors send. A signed-in visitor
//     allowed to save adds one (POST); the owner reads all; each visitor
//     reads (?mine=1), changes (PATCH) and withdraws (DELETE) only their own,
//     and can undo their own withdrawal for SAVED_DATA_WITHDRAW_UNDO_MINUTES.
//     Private to the owner unless declared visibility "public". Options:
//     one entry per person, and email to the owner on new entries.
//
// Who may save (a site setting): anyone who signs in (default), or only
// listed emails and @domains; a block list applies either way. It covers
// every visitor write on the site, page data and lists included.
//
// A name nobody declared is Shared (owner decision 2026-09-27): anyone reads
// it and signed-in visitors save to it, as before the kinds, so older skills,
// Uploaded pages keep working. Page info and Submissions are
// upgrades the owner's agent declares. SAVED_DATA_DEFAULT_KIND=declare_first
// makes an undeclared name take no saves at all (409 declare_first) on sites
// made after the kinds; sites made before them (sites.legacy_data) stay
// Shared either way.

var kindLabels = map[string]string{model.KindContent: "Page info", model.KindEntries: "Submissions", model.KindPersonal: "Personal", model.KindBoard: "Shared board"}

func kindLabel(kind string) string {
	if l, ok := kindLabels[kind]; ok {
		return l
	}
	return "Not set"
}

// nameLabel is a name's kind in product words, undeclared names included:
// Shared where they take saves, a private list from before the kinds, or Not
// set (declare_first installs).
func nameLabel(kind string, private, shared bool) string {
	switch {
	case kind != "":
		return kindLabel(kind)
	case private:
		return "Private list"
	case shared:
		return "Shared"
	}
	return "Not set"
}

// writeDeclareFirst is the answer for a save to a name that has no kind on a
// site that needs one.
func writeDeclareFirst(w http.ResponseWriter, siteName, name string) {
	writeJSON(w, http.StatusConflict, errorResponse{
		Error: fmt.Sprintf("nothing can be saved under %q yet: the site owner first says what it is. "+
			"Declare it with PUT /v1/sites/%s/data/%s/kind and {\"kind\": \"entries\"} for things visitors send (RSVPs, orders, sign-ups, votes, comments), "+
			"or {\"kind\": \"content\"} for page info only the owner writes (menu, schedule, prices)", name, siteName, name),
		Code: "declare_first",
	})
}

func writeWrongKind(w http.ResponseWriter, name, kind, hint string) {
	writeJSON(w, http.StatusConflict, errorResponse{
		Error: fmt.Sprintf("%q is %s (kind %q): %s", name, kindLabel(kind), kind, hint),
		Code:  "wrong_kind",
	})
}

func writeNotAllowedToSave(w http.ResponseWriter) {
	writeJSON(w, http.StatusForbidden, errorResponse{
		Error: "the owner of this site has not allowed this account to save here",
		Code:  "not_allowed_to_save",
	})
}

// dataSettings reads name's declaration on siteID (500 written on failure).
func (h *SiteHandler) dataSettings(w http.ResponseWriter, r *http.Request, siteID, name string) (model.DataSettings, bool) {
	set, err := h.legacySvc().WriteCollectionSettings(r.Context(), siteID, name, h.savedData.DefaultKind == legacyDeclareFirst)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
		return set, false
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return set, false
	}
	return set, true
}

// entriesLike: the name holds Submissions, declared or (on a legacy site)
// an undeclared list that behaves as before.
func entriesLike(set model.DataSettings) bool {
	return (set.Kind == "entries" || (set.Kind == "" && set.Shared))
}

// listLike: the name is a list of items anyone may be shown (Submissions,
// Shared) or a Shared board.
func listLike(set model.DataSettings) bool {
	return entriesLike(set) || set.Kind == model.KindBoard
}

// appendAllowed is the kind check before any item is added to a name. Writes
// the refusal.
func (h *SiteHandler) appendAllowed(w http.ResponseWriter, set model.DataSettings, siteName string) bool {
	switch {
	case set.Kind == model.KindContent:
		writeWrongKind(w, set.Name, set.Kind, "only the owner saves it, as a whole document with PUT /v1/sites/"+siteName+"/data/"+set.Name)
		return false
	case set.Kind == model.KindPersonal:
		writeWrongKind(w, set.Name, set.Kind, "each signed-in visitor saves their own record with PUT or PATCH /v1/sites/"+siteName+"/data/"+set.Name+" (SH.data(name, 'personal').set)")
		return false
	case set.Kind == "" && !set.Shared:
		writeDeclareFirst(w, siteName, set.Name)
		return false
	}
	return true
}

// entryMaxBytes is the largest item a name takes: SAVED_DATA_ENTRY_MAX_KB for
// declared Submissions, the list limit for a legacy list.
func (h *SiteHandler) entryMaxBytes(set model.DataSettings) int64 {
	return legacy.EntryMaxBytes(set, h.savedData.EntryMaxKB, maxCollectionItemSize)
}

// saveEntry stores one new item in a declared Submissions name with its rules
// (one per person, the per-name cap), or appends to a legacy list. Writes the
// answer on a refusal; ok=false then.
func (h *SiteHandler) saveEntry(w http.ResponseWriter, r *http.Request, siteID string, set model.DataSettings, body []byte, a model.Actor) (model.CollectionItem, bool) {
	item, have, err := h.legacySvc().SaveEntry(r.Context(), legacy.SaveEntryInput{SiteID: siteID, Settings: set, Body: body, Actor: a, MaxEntries: h.savedData.EntriesMax})
	return h.entryResult(w, set, item, have, err)
}

func (h *SiteHandler) entryResult(w http.ResponseWriter, set model.DataSettings, item model.CollectionItem, have int64, err error) (model.CollectionItem, bool) {
	switch {
	case errors.Is(err, model.ErrOnePerPerson) && have == 0:
		writeJSON(w, http.StatusConflict, errorResponse{
			Error: "this address already has an entry here (one per person), sent from another sign-in; change or withdraw it from there",
			Code:  "one_per_person",
		})
	case errors.Is(err, model.ErrOnePerPerson):
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": "you already have an entry here (one per person): change it with PATCH .../items/" + strconv.FormatInt(have, 10) + " or withdraw it first",
			"code":  "one_per_person", "id": have,
		})
	case errors.Is(err, model.ErrNameFull):
		writeJSON(w, http.StatusConflict, errorResponse{
			Error: fmt.Sprintf("this list is full (%d entries); the owner can delete entries or clear it to make room", h.savedData.EntriesMax),
			Code:  "list_full",
		})
	case errors.Is(err, model.ErrKindChanged):
		writeKindChanged(w, set.Name)
	case errors.Is(err, sql.ErrNoRows):
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
	default:
		return item, true
	}
	return item, false
}

// writeKindChanged: the owner changed what the name is while this save was
// on its way; nothing was saved.
func writeKindChanged(w http.ResponseWriter, name string) {
	writeJSON(w, http.StatusConflict, errorResponse{
		Error: fmt.Sprintf("the site owner just changed what %q is, so nothing was saved; load the page again and retry", name),
		Code:  "kind_changed",
	})
}

// saverOK applies the site's who-may-save setting to a visitor write. email
// is the visitor's verified address ("" when nobody is signed in). Writes the
// refusal.
func (h *SiteHandler) saverOK(w http.ResponseWriter, r *http.Request, siteID, email string) bool {
	ok, err := h.legacySvc().SaverAllowed(r.Context(), legacy.SaverInput{SiteID: siteID, Email: email})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return false
	}
	if !ok {
		writeNotAllowedToSave(w)
	}
	return ok
}

// personSaverOK applies who-may-save to a signed-in person (userID "" for
// nobody signed in). A failed address lookup refuses (500): it never counts
// as an address nobody blocked. The site's owner may unless using an internal
// visitor pass. Writes the
// refusal.
func (h *SiteHandler) personSaverOK(w http.ResponseWriter, r *http.Request, siteID, userID string) bool {
	ok, err := h.legacySvc().PersonSaverAllowed(r.Context(), legacy.PersonSaverInput{SiteID: siteID, UserID: userID, VisitorPass: r.Header.Get(visitorPassHeader) != ""})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return false
	}
	if ok {
		return true
	}
	writeNotAllowedToSave(w)
	return false
}

// visitorWriteOK is the gate every page write passes (visitorWriteGate), then
// the site's who-may-save setting for anyone writing without the owner's key.
func (h *SiteHandler) visitorWriteSvc(r *http.Request, in legacy.VisitorWriteInput) *legacy.Adapter {
	return legacy.New(h.database, h.legacyStorageCutoff, legacy.Deps{
		VisitorOwnerKeyWrite:   func(owner string) bool { return h.ownerKeyWrite(r, owner) },
		VisitorDomainElsewhere: func(site string) (string, bool) { return h.livesOnDomainElsewhere(r, site) },
		VisitorOwnAddress: func(ctx context.Context, siteID string) (model.SiteDomainInfo, bool, error) {
			info, ok, err := h.siteOwnAddress(ctx, siteID)
			return model.SiteDomainInfo(info), ok, err
		},
		VisitorInternalSession: func(site string) (model.VisitorSession, bool) { return h.internalVisitorSession(r, site) },
		VisitorResolveKey:      h.resolveWriterKey,
		VisitorKeyActor:        func(owner string) model.Actor { return h.keyActor(r, owner) },
		VisitorSessionValid:    func(sess model.VisitorSession, site string) bool { return h.sessionValidFor(r, sess, site) },
		VisitorLog: func(mode, outcome string) {
			h.logAnonWrite(r, in.SiteID, in.SiteName, in.Route, in.Collection, mode, outcome)
		},
		VisitorLogKey: func(user string, allowed bool) {
			if allowed {
				log.Printf("key_write user_id=%s site_id=%s name=%s route=%s collection=%s", user, in.SiteID, in.SiteName, in.Route, in.Collection)
			} else {
				log.Printf("key_write_refused user_id=%s site_id=%s name=%s route=%s collection=%s", user, in.SiteID, in.SiteName, in.Route, in.Collection)
			}
		},
	})
}

func (h *SiteHandler) visitorWriteOK(w http.ResponseWriter, r *http.Request, siteID, siteName, route, collection string) (model.Actor, bool) {
	in := legacy.VisitorWriteInput{
		VisitorSessionCookie: h.sessionCookieFor(r),
		SiteID:               siteID, SiteName: siteName, Route: route, Collection: collection,
		Mode: h.writeAuthMode, Key: r.Header.Get("X-API-Key"),
		Preview: previewReferer(r), ContentHost: strings.EqualFold(requestHostName(r), h.contentHost),
		Canonical: h.personHostsCanonical(), VisitorPass: r.Header.Get(visitorPassHeader) != "",
		CSRFValid:   r.Header.Get(visitorCSRFHeader) == visitorCSRFValue,
		SessionIdle: h.limits.VisitorSessionIdle, KeyIdle: h.limits.KeyIdleExpiry,
	}
	actor, err := h.visitorWriteSvc(r, in).VisitorWrite(r.Context(), in)
	var e *legacy.Error
	switch {
	case err == nil:
		return actor, true
	case errors.As(err, &e) && e.Code == "site_not_found":
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
	case errors.As(err, &e) && e.Code == "site_suspended":
		writeSiteSuspended(w, e.Message)
	case errors.As(err, &e) && e.Code == "site_offline":
		writeSiteOffline(w)
	case errors.As(err, &e) && e.Code == "preview_read_only":
		writePreviewReadOnly(w)
	case errors.As(err, &e) && e.Code == "use_custom_domain":
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "this site saves on its own domain", "code": "use_custom_domain", "domain": e.Message})
	case errors.As(err, &e) && e.Code == "visitor_pass_forbidden":
		storageError(w, http.StatusForbidden, "forbidden", "visitor pass does not cover this site")
	case errors.As(err, &e) && e.Code == "pass_csrf_required":
		storageError(w, http.StatusForbidden, "csrf_required", "missing CSRF header")
	case errors.As(err, &e) && e.Code == "invalid_api_key":
		writeJSON(w, http.StatusUnauthorized, struct {
			Error string `json:"error"`
			Code  string `json:"code"`
		}{Error: "invalid API key", Code: "invalid_api_key"})
	case errors.As(err, &e) && e.Code == "account_suspended":
		writeAccountSuspended(w)
	case errors.As(err, &e) && e.Code == "csrf_required":
		writeJSON(w, http.StatusForbidden, struct {
			Error string `json:"error"`
			Code  string `json:"code"`
		}{Error: "missing CSRF header", Code: "csrf_required"})
	case errors.As(err, &e) && e.Code == "visitor_auth_required":
		writeVisitorAuthRequired(w)
	case errors.As(err, &e) && e.Code == "not_allowed_to_save":
		writeNotAllowedToSave(w)
	default:
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
	}
	return actor, false
}

// ---- the page-facing routes -----------------------------------------------------

// readSite resolves a page-facing read: the owner's key names the site
// exactly; a request naming a page passes the Origin gate; a request naming
// none (curl, an agent) is served as public reads are. ownerKey reports the
// first. Applies the read limit (unless the caller hands the read to a
// handler that counts it itself) and, for anyone but the owner, taken-down
// and offline. Writes the answer on failure.
func (h *SiteHandler) readSitePrelude(w http.ResponseWriter, r *http.Request, siteName, name string, countRead bool) (siteID string, ownerKey, ok bool) {
	if siteName == "" || !validCollectionName.MatchString(name) {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid site or data name"})
		return "", false, false
	}
	if id, isOwner := h.ownerSiteIDFromKey(r, siteName); isOwner {
		siteID, ownerKey = id, true
	} else {
		if !noBrowserOrigin(r) && !h.collectionGate(w, r, siteName, name) {
			return "", false, false
		}
		var err error
		if siteID, err = h.resolveSiteID(r, siteName); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
				return "", false, false
			}
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
			return "", false, false
		}
	}
	if countRead && !h.allowRead(w, r, siteID) {
		return "", false, false
	}
	return siteID, ownerKey, true
}

func (h *SiteHandler) readSite(w http.ResponseWriter, r *http.Request, siteName, name string, countRead bool) (string, bool, bool) {
	siteID, ownerKey, ok := h.readSitePrelude(w, r, siteName, name, countRead)
	if !ok {
		return "", false, false
	}
	if !ownerKey && (h.refuseSuspendedSiteID(w, r, siteID) || h.refuseOffline(w, r, siteID)) {
		return "", false, false
	}
	return siteID, ownerKey, true
}

// getDataKind is GET /v1/sites/{s}/data/{name}/kind: what a name is, for the
// page (auth.js checks it). Public: a kind is not a secret; the email choice
// is the owner's and is not shown.
func (h *SiteHandler) getDataKind(w http.ResponseWriter, r *http.Request) {
	siteName := strings.TrimSpace(r.PathValue("sitename"))
	name := strings.TrimSpace(r.PathValue("coll"))
	headers := http.Header{}
	svc := legacy.New(h.database, h.legacyStorageCutoff, legacy.Deps{
		KindOwnerSite:     func(name string) (string, bool) { return h.ownerSiteIDFromKey(r, name) },
		KindOriginAllowed: func(name string) bool { return h.authorizeStateOrigin(&dataReadHeaders{headers}, r, name) },
		KindResolveSite:   func(name string) (string, error) { return h.resolveSiteID(r, name) },
		KindReadLimit: func(id string) bool {
			return h.readLimiter == nil || h.readLimiter.allowHTTP(&dataReadHeaders{headers}, h.readBucket(r, id))
		},
	})
	kind, err := svc.GetDataKind(r.Context(), legacy.GetDataKindInput{SiteName: siteName, Name: name, ValidName: validCollectionName.MatchString(name), Originless: noBrowserOrigin(r), DeclareFirst: h.savedData.DefaultKind == legacyDeclareFirst})
	for key, values := range headers {
		w.Header()[key] = values
	}
	var serviceErr *legacy.Error
	if errors.As(err, &serviceErr) && serviceErr.Code == "invalid_site_or_name" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid site or data name"})
		return
	}
	if errors.As(err, &serviceErr) && serviceErr.Code == "origin_not_allowed" {
		writeOriginRefused(w)
		return
	}
	if errors.As(err, &serviceErr) && serviceErr.Code == "rate_limited" {
		tooManyRequests(w)
		return
	}
	if errors.As(err, &serviceErr) && serviceErr.Code == "site_suspended" {
		writeSiteSuspended(w, serviceErr.Message)
		return
	}
	if errors.As(err, &serviceErr) && serviceErr.Code == "site_offline" {
		writeSiteOffline(w)
		return
	}
	if errors.Is(err, sql.ErrNoRows) || (errors.As(err, &serviceErr) && serviceErr.Code == "site_not_found") {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	set := kind.Settings
	resp := map[string]any{"name": name, "declared": set.Kind != "", "kind": nil, "label": nameLabel(set.Kind, set.Private, set.Shared)}
	if set.Kind != "" {
		resp["kind"] = set.Kind
	}
	if kind.EntriesLike {
		resp["visibility"] = visibilityWord(set.Private)
		resp["one_per_person"] = kind.OnePerPerson
	}
	if set.Kind == "" {
		resp["accepts_saves"] = set.Shared
	}
	writeJSON(w, http.StatusOK, resp)
}

func visibilityWord(private bool) string {
	if private {
		return "owner"
	}
	return "public"
}

// getData is GET /v1/sites/{s}/data/{name}: Page info answers its document;
// Submissions answer as the list does (the owner reads all; everyone reads a
// public one), with ?mine=1 for a signed-in visitor's own entries and
// ?count=1 for how many there are.
func (h *SiteHandler) getData(w http.ResponseWriter, r *http.Request) {
	siteName := strings.TrimSpace(r.PathValue("sitename"))
	name := strings.TrimSpace(r.PathValue("coll"))
	q := r.URL.Query()
	if q.Get("mine") != "" {
		h.listMine(w, r, siteName, name)
		return
	}
	// A list read is counted by listCollection itself; everything else here.
	headers := http.Header{}
	svc := legacy.New(h.database, h.legacyStorageCutoff, legacy.Deps{
		KindOwnerSite:      func(name string) (string, bool) { return h.ownerSiteIDFromKey(r, name) },
		KindOriginAllowed:  func(name string) bool { return h.authorizeStateOrigin(&dataReadHeaders{headers}, r, name) },
		KindResolveSite:    func(name string) (string, error) { return h.resolveSiteID(r, name) },
		DataOwnerBrowser:   func(id string) bool { return h.ownerBrowserView(r, id) },
		ResolveMineSession: func(id string) (model.VisitorSession, bool) { return h.strictVisitorSession(r, id) },
		DataAdminMismatch:  func(id string) bool { return adminSiteMismatch(r, id) },
		DataPrivateRead: func(ctx context.Context, id string) (model.VisitorSession, error) {
			if strings.EqualFold(requestHostName(r), h.contentHost) {
				return model.VisitorSession{}, &legacy.Error{Code: "private_not_found"}
			}
			info, here, err := h.onOwnDomain(r, id)
			if err != nil {
				return model.VisitorSession{}, err
			}
			if !here {
				return model.VisitorSession{}, &legacy.Error{Code: "private_not_found"}
			}
			sess, ok := h.strictVisitorSession(r, id)
			if !ok || sess.UserID != info.OwnerID || r.Header.Get(visitorPassHeader) != "" {
				return model.VisitorSession{}, &legacy.Error{Code: "private_not_found"}
			}
			return sess, nil
		},
		DataReadLimit: func(id string) bool {
			return h.readLimiter == nil || h.readLimiter.allowHTTP(&dataReadHeaders{headers}, h.readBucket(r, id))
		},
	})
	result, err := svc.ReadData(r.Context(), legacy.DataReadInput{GetDataKindInput: legacy.GetDataKindInput{SiteName: siteName, Name: name, ValidName: validCollectionName.MatchString(name), Originless: noBrowserOrigin(r), DeclareFirst: h.savedData.DefaultKind == legacyDeclareFirst}, Count: q.Get("count") != "", ContentHost: strings.EqualFold(requestHostName(r), h.contentHost), Access: legacy.VisitorAccessInput{Idle: h.limits.VisitorSessionIdle}, Limit: q.Get("limit"), Before: q.Get("before")})
	for key, values := range headers {
		w.Header()[key] = values
	}
	if result.PrivateCache {
		w.Header().Set("Cache-Control", "private, no-store")
	}
	if result.WithAuthor {
		w.Header().Add("Vary", "Cookie")
		w.Header().Add("Vary", "X-API-Key")
	}
	var serviceErr *legacy.Error
	switch {
	case errors.As(err, &serviceErr) && serviceErr.Code == "invalid_site_or_name":
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid site or data name"})
		return
	case errors.As(err, &serviceErr) && serviceErr.Code == "origin_not_allowed":
		writeOriginRefused(w)
		return
	case errors.As(err, &serviceErr) && serviceErr.Code == "personal_only":
		writePersonalOnly(w, name)
		return
	case errors.As(err, &serviceErr) && serviceErr.Code == "visitor_auth_required":
		writeVisitorAuthRequired(w)
		return
	case errors.As(err, &serviceErr) && serviceErr.Code == "account_suspended":
		writeAccountSuspended(w)
		return
	case errors.As(err, &serviceErr) && serviceErr.Code == "site_suspended":
		writeSiteSuspended(w, serviceErr.Message)
		return
	case errors.As(err, &serviceErr) && serviceErr.Code == "site_offline":
		writeSiteOffline(w)
		return
	case errors.As(err, &serviceErr) && serviceErr.Code == "rate_limited":
		tooManyRequests(w)
		return
	case errors.As(err, &serviceErr) && serviceErr.Code == "private_not_found":
		writePrivateNotFound(w)
		return
	case errors.Is(err, sql.ErrNoRows) || (errors.As(err, &serviceErr) && serviceErr.Code == "site_not_found"):
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
		return
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	switch result.Route {
	case model.LegacyDataPersonal:
		if result.PersonalCount {
			if result.PersonalFew {
				writeJSON(w, http.StatusOK, map[string]any{"name": name, "count": 0, "few": true})
			} else {
				writeJSON(w, http.StatusOK, map[string]any{"name": name, "count": result.Count})
			}
		} else {
			writeJSONETag(w, r, personalBody(name, result.Document, result.Found))
		}
	case model.LegacyDataContent:
		resp := map[string]any{"name": name, "kind": model.KindContent, "data": nil}
		if result.Found {
			if result.OwnerView {
				resp["data"] = result.Document.Data
			} else {
				resp["data"] = withoutSubmitter(result.Document.Data)
			}
			resp["saved_at"] = result.Document.CreatedAt
		}
		writeJSON(w, http.StatusOK, resp)
	case model.LegacyDataUndeclared:
		writeDeclareFirst(w, siteName, name)
	case model.LegacyDataCount:
		writeJSON(w, http.StatusOK, map[string]any{"name": name, "count": result.Count})
	default:
		resp := map[string]any{"items": result.Items, "next": result.Next}
		if result.Private {
			resp["private"] = true
		}
		if result.Board {
			resp["kind"] = model.KindBoard
			writeJSONETag(w, r, resp)
			return
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

// dataReadHeaders collects rate-limit headers until the adapter returns.
type dataReadHeaders struct{ header http.Header }

func (h *dataReadHeaders) Header() http.Header       { return h.header }
func (h *dataReadHeaders) Write([]byte) (int, error) { panic("unexpected write") }
func (h *dataReadHeaders) WriteHeader(int)           { panic("unexpected status") }

// visitorOnSite is the visitor signed in to siteID on its own address, from a
// page there (the __Host- cookie, same origin), with the CSRF header when
// needCSRF. Writes 401/403 and ok=false otherwise.
func (h *SiteHandler) visitorOnSite(w http.ResponseWriter, r *http.Request, siteID string, needCSRF bool) (model.VisitorSession, bool) {
	if strings.EqualFold(requestHostName(r), h.contentHost) {
		writeVisitorAuthRequired(w)
		return model.VisitorSession{}, false
	}
	sess, ok := h.strictVisitorSession(r, siteID)
	if !ok {
		writeVisitorAuthRequired(w)
		return sess, false
	}
	err := h.legacySvc().CheckVisitorAccess(r.Context(), legacy.VisitorAccessInput{
		UserID: sess.UserID, SessionID: sess.ID, NeedCSRF: needCSRF,
		CSRFValid: r.Header.Get(visitorCSRFHeader) == visitorCSRFValue, Idle: h.limits.VisitorSessionIdle,
	})
	var serviceErr *legacy.Error
	switch {
	case errors.As(err, &serviceErr) && serviceErr.Code == "account_suspended":
		writeAccountSuspended(w)
		return sess, false
	case errors.As(err, &serviceErr) && serviceErr.Code == "csrf_required":
		writeJSON(w, http.StatusForbidden, errorResponse{Error: "missing CSRF header", Code: "csrf_required"})
		return sess, false
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return sess, false
	}
	return sess, true
}

func (h *SiteHandler) mineSvc(r *http.Request, headers http.Header) *legacy.Adapter {
	return legacy.New(h.database, h.legacyStorageCutoff, legacy.Deps{
		ResolveMineSession: func(id string) (model.VisitorSession, bool) { return h.strictVisitorSession(r, id) },
		MineResolveSite:    func(name string) (string, error) { return h.resolveSiteID(r, name) },
		MineReadLimit: func(id string) bool {
			return h.readLimiter == nil || h.readLimiter.allowHTTP(&dataReadHeaders{headers}, h.readBucket(r, id))
		},
	})
}

// listMine is GET .../data/{name}?mine=1: the signed-in visitor's own live
// entries, newest first (?limit, ?before as for lists).
func (h *SiteHandler) listMine(w http.ResponseWriter, r *http.Request, siteName, name string) {
	w.Header().Set("Cache-Control", "private, no-store")
	if siteName == "" || !validCollectionName.MatchString(name) {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid site or data name"})
		return
	}
	limit, before := pageArgs(r)
	headers := http.Header{}
	result, err := h.mineSvc(r, headers).ListMine(r.Context(), legacy.ListMineInput{
		SiteName: siteName, Name: name, DeclareFirst: h.savedData.DefaultKind == legacyDeclareFirst,
		ContentHost: strings.EqualFold(requestHostName(r), h.contentHost),
		Access:      legacy.VisitorAccessInput{Idle: h.limits.VisitorSessionIdle}, Limit: limit, Before: before,
	})
	for key, values := range headers {
		w.Header()[key] = values
	}
	var serviceErr *legacy.Error
	switch {
	case errors.As(err, &serviceErr) && serviceErr.Code == "rate_limited":
		tooManyRequests(w)
		return
	case errors.As(err, &serviceErr) && serviceErr.Code == "site_suspended":
		writeSiteSuspended(w, serviceErr.Message)
		return
	case errors.As(err, &serviceErr) && serviceErr.Code == "visitor_auth_required":
		writeVisitorAuthRequired(w)
		return
	case errors.As(err, &serviceErr) && serviceErr.Code == "account_suspended":
		writeAccountSuspended(w)
		return
	case errors.As(err, &serviceErr) && serviceErr.Code == "wrong_kind":
		if result.Settings.Kind == model.KindContent || result.Settings.Kind == model.KindPersonal {
			writeWrongKind(w, name, result.Settings.Kind, "read it with GET /v1/sites/"+siteName+"/data/"+name)
		} else {
			writeDeclareFirst(w, siteName, name)
		}
		return
	case errors.As(err, &serviceErr) && serviceErr.Code == "site_not_found":
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
		return
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": result.Items, "next": result.Next, "mine": true})
}

// putContent is PUT /v1/sites/{s}/data/{name}: the owner replaces a Page info
// document (one JSON object, at most SAVED_DATA_CONTENT_MAX_KB). The earlier
// document goes to the name's history.
func (h *SiteHandler) contentSvc(r *http.Request, headers http.Header) *legacy.Adapter {
	return legacy.New(h.database, h.legacyStorageCutoff, legacy.Deps{
		ContentSiteMaxBytes: h.siteMaxBytes, BoundHistory: h.boundHistoryContext,
		ContentDeclareFirst: func() bool { return h.savedData.DefaultKind == legacyDeclareFirst },
		VisitorResolveKey:   h.resolveWriterKey,
		ContentOwnerBrowser: func(siteID string) bool { return h.ownerBrowserView(r, siteID) },
		ContentAuthor:       h.withAuthorEmail,
		PersonalSession:     func(id string) (model.VisitorSession, bool) { return h.strictVisitorSession(r, id) },
		PersonalWriteLimit: func() bool {
			return h.appendLimiter == nil || h.appendLimiter.allowHTTP(&dataReadHeaders{headers}, clientIP(r))
		},
		PersonalReadBody: func() ([]byte, []byte, error) {
			raw, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, int64(h.savedData.PersonalMaxKB)<<10))
			if err != nil {
				var maxErr *http.MaxBytesError
				if errors.As(err, &maxErr) {
					return nil, nil, &legacy.Error{Code: "personal_too_large"}
				}
				return nil, nil, &legacy.Error{Code: "invalid_body"}
			}
			var obj map[string]json.RawMessage
			dec := json.NewDecoder(bytes.NewReader(raw))
			if err := dec.Decode(&obj); err != nil || obj == nil || dec.More() {
				return nil, nil, &legacy.Error{Code: "personal_not_object"}
			}
			if !jsonStorable(raw) {
				return nil, nil, &legacy.Error{Code: "invalid_json"}
			}
			return raw, bytes.TrimSpace(raw), nil
		},
		ContentBody: func() ([]byte, error) {
			maxBytes := int64(h.savedData.ContentMaxKB) << 10
			raw, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, maxBytes))
			if err != nil {
				var maxErr *http.MaxBytesError
				if errors.As(err, &maxErr) {
					return nil, &legacy.Error{Code: "content_too_large"}
				}
				return nil, &legacy.Error{Code: "invalid_body"}
			}
			var obj map[string]json.RawMessage
			dec := json.NewDecoder(bytes.NewReader(raw))
			if err := dec.Decode(&obj); err != nil || obj == nil || dec.More() {
				return nil, &legacy.Error{Code: "content_not_object"}
			}
			if !jsonStorable(raw) {
				return nil, &legacy.Error{Code: "invalid_json"}
			}
			return bytes.TrimSpace(raw), nil
		},
	})
}

func (h *SiteHandler) putContent(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	siteName := strings.TrimSpace(r.PathValue("sitename"))
	name := strings.TrimSpace(r.PathValue("coll"))
	// A key needs no page Origin here: it is the authorization, and a
	// browser never sends one on its own. A request from a page is gated.
	if r.Header.Get("X-API-Key") != "" && noBrowserOrigin(r) {
		if siteName == "" || !validCollectionName.MatchString(name) {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid site or data name"})
			return
		}
	} else if !h.collectionGate(w, r, siteName, name) {
		return
	}
	siteID, err := h.resolveWriteSiteID(r, siteName)
	if err != nil {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
		return
	}
	headers := http.Header{}
	result, err := h.contentSvc(r, headers).PutData(r.Context(), legacy.PutDataInput{PutContentInput: legacy.PutContentInput{
		SiteID: siteID, Name: name, Key: r.Header.Get("X-API-Key"),
		CSRFValid: r.Header.Get(visitorCSRFHeader) == visitorCSRFValue,
	}, Preview: previewReferer(r), ContentHost: strings.EqualFold(requestHostName(r), h.contentHost), VisitorPass: r.Header.Get(visitorPassHeader) != "", Access: legacy.VisitorAccessInput{NeedCSRF: true, CSRFValid: r.Header.Get(visitorCSRFHeader) == visitorCSRFValue, Idle: h.limits.VisitorSessionIdle}, IdempotencyKey: r.Header.Get("Idempotency-Key"), PersonalPeopleMax: h.savedData.PersonalPeopleMax})
	for key, values := range headers {
		w.Header()[key] = values
	}
	if result.Replayed {
		w.Header().Set("Idempotent-Replayed", "true")
	}
	var serviceErr *legacy.Error
	switch {
	case errors.As(err, &serviceErr) && serviceErr.Code == "site_suspended":
		writeSiteSuspended(w, serviceErr.Message)
	case errors.As(err, &serviceErr) && serviceErr.Code == "personal_only":
		writePersonalOnly(w, name)
	case errors.As(err, &serviceErr) && serviceErr.Code == "site_offline":
		writeSiteOffline(w)
	case errors.As(err, &serviceErr) && serviceErr.Code == "preview_read_only":
		writePreviewReadOnly(w)
	case errors.As(err, &serviceErr) && serviceErr.Code == "visitor_auth_required":
		writeVisitorAuthRequired(w)
	case errors.As(err, &serviceErr) && serviceErr.Code == "account_suspended":
		writeAccountSuspended(w)
	case errors.As(err, &serviceErr) && serviceErr.Code == "not_allowed_to_save":
		writeNotAllowedToSave(w)
	case errors.As(err, &serviceErr) && serviceErr.Code == "rate_limited":
		tooManyRequests(w)
	case errors.As(err, &serviceErr) && serviceErr.Code == "personal_too_large":
		_, _ = http.MaxBytesReader(w, io.NopCloser(strings.NewReader("x")), 0).Read(make([]byte, 1))
		rep := h.personalTooLarge()
		writeJSON(w, rep.status, errorResponse{Error: rep.msg, Code: rep.code})
	case errors.As(err, &serviceErr) && serviceErr.Code == "personal_not_object":
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "a personal record is one JSON object, e.g. {\"streak\": 3}", Code: "not_an_object"})
	case errors.As(err, &serviceErr) && serviceErr.Code == "invalid_idempotency_key":
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "Idempotency-Key is at most 255 characters", Code: "invalid_idempotency_key"})
	case errors.As(err, &serviceErr) && serviceErr.Code == "idempotency_key_reused":
		writeJSON(w, http.StatusConflict, errorResponse{Error: "this Idempotency-Key was already used for a different request; use a new key for a new write", Code: "idempotency_key_reused"})
	case errors.As(err, &serviceErr) && serviceErr.Code == "idempotency_in_progress":
		writeJSON(w, http.StatusConflict, errorResponse{Error: "a request with this Idempotency-Key is still being saved; retry in a moment", Code: "idempotency_in_progress"})
	case errors.As(err, &serviceErr) && serviceErr.Code == "content_wrong_kind":
		writeWrongKind(w, name, serviceErr.Message, "add items with POST /v1/sites/"+siteName+"/data/"+name+"; only the owner empties it, with DELETE /v1/sites/"+siteName+"/collections/"+name)
	case errors.As(err, &serviceErr) && serviceErr.Code == "content_declare_first":
		writeJSON(w, http.StatusConflict, errorResponse{
			Error: fmt.Sprintf("%q is not page info yet: declare it first with PUT /v1/sites/%s/data/%s/kind and {\"kind\": \"content\"}, then save it here", name, siteName, name),
			Code:  "declare_first",
		})
	case errors.As(err, &serviceErr) && serviceErr.Code == "site_not_found":
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
	case errors.As(err, &serviceErr) && serviceErr.Code == "invalid_api_key":
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "invalid API key", Code: "invalid_api_key"})
	case errors.As(err, &serviceErr) && serviceErr.Code == "csrf_required":
		writeJSON(w, http.StatusForbidden, errorResponse{Error: "missing CSRF header", Code: "csrf_required"})
	case errors.As(err, &serviceErr) && serviceErr.Code == "owner_only":
		writeJSON(w, http.StatusForbidden, errorResponse{Error: "only the site owner can change page info", Code: "owner_only"})
	case errors.As(err, &serviceErr) && serviceErr.Code == "content_too_large":
		// MaxBytesReader marks an HTTP/1 response for connection close when a body exceeds the limit.
		_, _ = http.MaxBytesReader(w, io.NopCloser(strings.NewReader("x")), 0).Read(make([]byte, 1))
		writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: fmt.Sprintf("page info is at most %d KB", h.savedData.ContentMaxKB), Code: "item_too_large"})
	case errors.As(err, &serviceErr) && serviceErr.Code == "invalid_body":
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
	case errors.As(err, &serviceErr) && serviceErr.Code == "content_not_object":
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "page info is one JSON object, e.g. {\"items\": [...]}", Code: "not_an_object"})
	case errors.As(err, &serviceErr) && serviceErr.Code == "invalid_json":
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: `the JSON holds text that cannot be saved: a NUL character (\u0000), half of a surrogate pair (\ud800 alone) or bytes that are not UTF-8. Remove it and send again`, Code: "invalid_json"})
	case result.Personal && errors.Is(err, sql.ErrNoRows):
		writeJSON(w, http.StatusConflict, errorResponse{Error: fmt.Sprintf("%q is not Personal any more", name), Code: "wrong_kind"})
	case errors.Is(err, model.ErrPeopleFull):
		writeJSON(w, http.StatusConflict, errorResponse{Error: fmt.Sprintf("%q already keeps a record for %d people, the most one Personal name holds, so it takes no one new; ask the site owner", name, h.savedData.PersonalPeopleMax), Code: "people_full"})
	case errors.Is(err, model.ErrSiteFull):
		h.writeSiteFull(w)
	case err != nil:
		if result.Personal && result.SaveAttempted {
			log.Printf("personal save site_id=%s name=%s: %v", siteID, name, err)
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
	default:
		if result.Personal {
			status := http.StatusOK
			if result.Replayed {
				status = result.ReplayStatus
			}
			writeJSON(w, status, personalBody(name, result.Item, result.Found))
		} else {
			writeJSON(w, http.StatusOK, map[string]any{"name": name, "kind": model.KindContent, "data": result.Item.Data, "saved_at": result.Item.CreatedAt})
		}
	}
}

// contentWriter is who may write Page info: the owner's (or the admin's) key
// or connector, or the owner signed in on the site's own address with the
// CSRF header. A key of another account gets the 404 of a missing site;
// anyone else 403 owner_only. Writes the refusal.
func (h *SiteHandler) contentWriter(w http.ResponseWriter, r *http.Request, siteID string) (model.Actor, bool) {
	ownerID, _, err := h.sitesSvc().GetSiteWriteGate(r.Context(), siteID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
		return model.Actor{}, false
	}
	if key := r.Header.Get("X-API-Key"); key != "" {
		u, ok, err := h.resolveWriterKey(r.Context(), key)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
			return model.Actor{}, false
		}
		if !ok {
			writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "invalid API key", Code: "invalid_api_key"})
			return model.Actor{}, false
		}
		if !u.IsAdmin && u.ID != ownerID {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return model.Actor{}, false
		}
		a := model.Actor{ID: u.ID, Kind: actorOwner}
		if u.ID != ownerID {
			a.Kind = actorAdmin
		}
		return h.withAuthorEmail(r.Context(), a), true
	}
	if h.ownerBrowserView(r, siteID) {
		if r.Header.Get(visitorCSRFHeader) != visitorCSRFValue {
			writeJSON(w, http.StatusForbidden, errorResponse{Error: "missing CSRF header", Code: "csrf_required"})
			return model.Actor{}, false
		}
		return h.withAuthorEmail(r.Context(), model.Actor{ID: ownerID, Kind: actorOwner}), true
	}
	writeJSON(w, http.StatusForbidden, errorResponse{Error: "only the site owner can change page info", Code: "owner_only"})
	return model.Actor{}, false
}

// updateEntry is PATCH /v1/sites/{s}/data/{name}/items/{id}. The owner (key,
// connector, or signed in on the site) goes to the list's owner edit; a
// signed-in visitor changes their own entry: the fields sent are merged in (a
// null removes one); _submitted_by and _submitted_at never change.
func (h *SiteHandler) updateEntry(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	in := h.entryRequest(r)
	siteName, name := in.SiteName, in.Name
	result, err := h.entrySvc(r).UpdateEntry(r.Context(), in)
	if h.writeEntrySelection(w, in, result, err, "update") {
		return
	}
	item, set := result.Item, result.Settings
	var serviceErr *legacy.Error
	switch {
	case errors.As(err, &serviceErr) && serviceErr.Code == "account_suspended":
		writeAccountSuspended(w)
	case errors.As(err, &serviceErr) && serviceErr.Code == "csrf_required":
		writeJSON(w, http.StatusForbidden, errorResponse{Error: "missing CSRF header", Code: "csrf_required"})
	case errors.As(err, &serviceErr) && serviceErr.Code == "site_not_found":
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
	case errors.As(err, &serviceErr) && serviceErr.Code == "wrong_kind":
		if set.Kind == model.KindContent {
			writeWrongKind(w, name, set.Kind, "only the owner changes it")
		} else {
			writeDeclareFirst(w, siteName, name)
		}
	case errors.As(err, &serviceErr) && serviceErr.Code == "invalid_id":
		writePrivateNotFound(w)
	case errors.As(err, &serviceErr) && serviceErr.Code == "not_found":
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "no entry of yours with that id", Code: "not_found"})
	case errors.As(err, &serviceErr) && serviceErr.Code == "not_allowed":
		writeNotAllowedToSave(w)
	case errors.As(err, &serviceErr) && serviceErr.Code == "item_too_large":
		signalLegacyBodyTooLarge(w)
		writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: "item too large", Code: "item_too_large"})
	case errors.As(err, &serviceErr) && serviceErr.Code == "invalid_patch":
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: `send a JSON object of the fields to change, e.g. {"guests": 3}`})
	case errors.As(err, &serviceErr) && serviceErr.Code == "invalid_json":
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: `the JSON holds text that cannot be saved: a NUL character (\u0000), half of a surrogate pair (\ud800 alone) or bytes that are not UTF-8. Remove it and send again`, Code: "invalid_json"})

	case errors.Is(err, sql.ErrNoRows):
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "no entry of yours with that id", Code: "not_found"})
	case errors.Is(err, model.ErrLegacyEntryTooLarge):
		writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: "item too large", Code: "item_too_large"})
	case errors.Is(err, model.ErrSiteFull):
		h.writeSiteFull(w)
	case errors.Is(err, model.ErrLegacyEntryNotObject):
		writeJSON(w, http.StatusConflict, errorResponse{Error: "this entry is not a JSON object, so it has no fields to change; withdraw it and send a new one", Code: "not_an_object"})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
	default:
		writeJSON(w, http.StatusOK, item)
	}
}

// withdrawEntry is DELETE /v1/sites/{s}/data/{name}/items/{id}. The owner goes
// to the list's owner delete; a signed-in visitor withdraws their own entry,
// which they can bring back (POST .../undo) for SAVED_DATA_WITHDRAW_UNDO_MINUTES
// and the owner for SAVED_DATA_UNDO_DAYS. A blocked visitor can still
// withdraw their own.
func (h *SiteHandler) withdrawEntry(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	in := h.entryRequest(r)
	siteName, name, id := in.SiteName, in.Name, in.ID
	result, err := h.entrySvc(r).WithdrawEntry(r.Context(), in)
	if h.writeEntrySelection(w, in, result, err, "withdraw") {
		return
	}
	set := result.Settings
	var serviceErr *legacy.Error
	switch {
	case errors.As(err, &serviceErr) && serviceErr.Code == "account_suspended":
		writeAccountSuspended(w)
		return
	case errors.As(err, &serviceErr) && serviceErr.Code == "csrf_required":
		writeJSON(w, http.StatusForbidden, errorResponse{Error: "missing CSRF header", Code: "csrf_required"})
		return
	case errors.As(err, &serviceErr) && serviceErr.Code == "wrong_kind":
		if set.Kind == model.KindContent {
			writeWrongKind(w, name, set.Kind, "only the owner changes it")
		} else {
			writeDeclareFirst(w, siteName, name)
		}
		return
	case errors.As(err, &serviceErr) && serviceErr.Code == "invalid_id":
		writePrivateNotFound(w)
		return
	case errors.As(err, &serviceErr) && serviceErr.Code == "not_found":
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "no entry of yours with that id", Code: "not_found"})
		return
	case errors.Is(err, sql.ErrNoRows):
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
		return
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"withdrawn": id, "undo_minutes": h.savedData.WithdrawUndoMinutes})
}

// undoWithdraw is POST /v1/sites/{s}/data/{name}/items/{id}/undo: a signed-in
// visitor brings back an entry they withdrew themselves, within
// SAVED_DATA_WITHDRAW_UNDO_MINUTES. (The owner restores anything from
// Recently deleted instead.)
func (h *SiteHandler) undoWithdraw(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	in := h.entryRequest(r)
	siteName, name := in.SiteName, in.Name
	window := in.Window
	result, err := h.entrySvc(r).UndoEntry(r.Context(), in)
	if h.writeEntrySelection(w, in, result, err, "undo") {
		return
	}
	item, done, set := result.Item, result.Done, result.Settings
	var serviceErr *legacy.Error
	switch {
	case errors.As(err, &serviceErr) && serviceErr.Code == "account_suspended":
		writeAccountSuspended(w)
		return
	case errors.As(err, &serviceErr) && serviceErr.Code == "csrf_required":
		writeJSON(w, http.StatusForbidden, errorResponse{Error: "missing CSRF header", Code: "csrf_required"})
		return
	case errors.As(err, &serviceErr) && serviceErr.Code == "wrong_kind":
		if set.Kind == model.KindContent {
			writeWrongKind(w, name, set.Kind, "only the owner changes it")
		} else {
			writeDeclareFirst(w, siteName, name)
		}
	case errors.As(err, &serviceErr) && serviceErr.Code == "invalid_id":
		writePrivateNotFound(w)
	case errors.As(err, &serviceErr) && serviceErr.Code == "not_found":
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "no entry of yours with that id", Code: "not_found"})
	case errors.As(err, &serviceErr) && serviceErr.Code == "not_allowed":
		writeNotAllowedToSave(w)
	case errors.As(err, &serviceErr) && serviceErr.Code == "site_not_found":
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
	case errors.Is(err, model.ErrOnePerPerson):
		writeJSON(w, http.StatusConflict, errorResponse{Error: "you have another entry here now (one per person); withdraw it first", Code: "one_per_person"})
	case errors.Is(err, model.ErrNameFull):
		writeJSON(w, http.StatusConflict, errorResponse{
			Error: fmt.Sprintf("this list is full (%d entries); the owner can delete entries or clear it to make room", h.savedData.EntriesMax),
			Code:  "list_full",
		})
	case errors.Is(err, model.ErrSiteFull):
		h.writeSiteFull(w)
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
	case !done:
		writeJSON(w, http.StatusConflict, errorResponse{
			Error: fmt.Sprintf("only an entry you withdrew yourself in the last %s can be brought back here; the site owner can restore older ones", storeSpan(window)),
			Code:  "undo_expired",
		})
	default:
		writeJSON(w, http.StatusOK, item)
	}
}

// optionsData answers a cross-origin preflight for the page-facing data routes.
func (h *SiteHandler) optionsData(w http.ResponseWriter, r *http.Request) {
	siteName := strings.TrimSpace(r.PathValue("sitename"))
	if siteName == "" || !h.authorizeStateOrigin(w, r, siteName) {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-SH-CSRF, Idempotency-Key, If-Match, If-None-Match")
	w.Header().Set("Access-Control-Max-Age", "600")
	w.WriteHeader(http.StatusNoContent)
}

// ---- the owner's routes -----------------------------------------------------------

// declareData is PUT /v1/sites/{s}/data/{name}/kind: the owner declares (or
// changes) what a name is.
//
//	{"kind": "entries", "visibility": "owner"|"public", "one_per_person": bool, "notify": "off"|"each"|"daily"}
//	{"kind": "content"}
//
// A field left out keeps what the name had; on a first declaration
// Submissions are private to the owner, and emailed daily when private (off
// when public).
func (h *SiteHandler) declareData(w http.ResponseWriter, r *http.Request) {
	_, siteID, siteName, name, ok := h.ownedDataSite(w, r)
	if !ok || h.refuseSuspendedSiteID(w, r, siteID) {
		return
	}
	var req struct {
		Kind         string `json:"kind"`
		Visibility   string `json:"visibility"`
		OnePerPerson *bool  `json:"one_per_person"`
		Notify       string `json:"notify"`
		// ConfirmPublic: the owner saw what becomes public (a private name
		// that holds entries, declared as something anyone reads).
		ConfirmPublic bool `json:"confirm_public"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: `send {"kind": "entries"} or {"kind": "content"}`, Code: "invalid_kind"})
		return
	}
	req.Kind = normalizeKind(req.Kind)
	prev, ok := h.dataSettings(w, r, siteID, name)
	if !ok {
		return
	}
	// Kinds that take saves only from signed-in visitors are not offered
	// where nobody can sign in (a name already declared keeps its kind).
	if req.Kind != prev.Kind && (req.Kind == model.KindEntries || req.Kind == model.KindPersonal || req.Kind == model.KindBoard) &&
		!h.signInNeededOK(w, map[string]string{model.KindEntries: "Submissions", model.KindPersonal: "Personal records", model.KindBoard: "Shared boards"}[req.Kind]) {
		return
	}
	// Personal records belong to the people who saved them: a Personal name
	// becomes another kind (which the owner or everyone reads) only when it
	// holds none, not even in Recently deleted.
	if prev.Kind == model.KindPersonal && req.Kind != model.KindPersonal && validKind(req.Kind) {
		has, err := h.legacySvc().NameHasRows(r.Context(), siteID, name)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
			return
		}
		if has {
			writeHasRecords(w, name)
			return
		}
	}
	switch req.Kind {
	case model.KindContent:
		if req.Visibility == "owner" || (req.OnePerPerson != nil && *req.OnePerPerson) || (req.Notify != "" && req.Notify != model.NotifyOff) {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "page info is always public and written only by the owner; visibility, one_per_person and notify apply to Submissions (kind entries)", Code: "invalid_kind"})
			return
		}
		if prev.Kind != model.KindContent {
			n, err := h.legacySvc().CountKindNames(r.Context(), siteID, model.KindContent, name)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
				return
			}
			if n >= h.savedData.ContentNamesMax {
				writeJSON(w, http.StatusConflict, errorResponse{Error: fmt.Sprintf("a site has at most %d page info names; keep related settings together in one document", h.savedData.ContentNamesMax), Code: "too_many_names"})
				return
			}
		}
		// A private list never becomes the page's public document (what
		// visitors sent there stays theirs and the owner's), and a list of
		// several entries never becomes one document: both checked under
		// the declaration lock.
		if !h.declareLocked(w, r, siteID, name, model.KindContent, false, false, model.NotifyOff, false, "page info") {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"site": siteName, "name": name, "kind": model.KindContent, "label": kindLabel(model.KindContent),
			"message": "Page info: anyone who can open the site reads it; only you change it (PUT /v1/sites/" + siteName + "/data/" + name + ", update_data).",
		})
	case model.KindEntries:
		// A first declaration starts from the defaults: private to the owner,
		// a daily email while private (off when public), any number per
		// person. A re-declaration keeps what it does not name.
		private, notify, one := true, "", false
		if prev.Kind == model.KindEntries {
			private, notify, one = prev.Private, prev.Notify, prev.OnePerPerson
		}
		switch req.Visibility {
		case "":
		case "owner", "private":
			private = true
		case "public":
			private = false
		default:
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: `visibility is "owner" (only you read them; the default) or "public" (anyone reads them)`, Code: "invalid_kind"})
			return
		}
		switch req.Notify {
		case "":
		case model.NotifyOff, model.NotifyEach, model.NotifyDaily:
			notify = req.Notify
		default:
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: `notify is "off", "each" (batched, soon after they arrive) or "daily" (a digest)`, Code: "invalid_kind"})
			return
		}
		if notify == "" {
			notify = model.NotifyDaily
			if !private || h.noEmail {
				notify = model.NotifyOff
			}
		}
		if notify != model.NotifyOff && h.noEmail && req.Notify != "" {
			writeJSON(w, http.StatusConflict, errorResponse{
				Error: `this server sends no email (RESEND_API_KEY is not set), so it cannot email you about new entries; send "notify": "off", and read them with GET .../data/` + name,
				Code:  "email_unavailable",
			})
			return
		}
		if req.OnePerPerson != nil {
			one = *req.OnePerPerson
		}
		home, hasHome, err := h.siteHomeFor(r.Context(), siteID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
			return
		}
		if private && !hasHome {
			writeJSON(w, http.StatusConflict, errorResponse{
				Error: strings.Replace(privateListNeedsDomain, "%s", h.handoutBase(), 1),
				Code:  "custom_domain_required",
			})
			return
		}
		if !hasHome {
			writeJSON(w, http.StatusConflict, errorResponse{
				Error: "Submissions come from visitors who sign in, and visitors sign in on the site's own address: connect one first (a free <name>." + h.handoutBase() + " address works, or your own domain)",
				Code:  "custom_domain_required",
			})
			return
		}
		if !private && !h.publicEntriesOK(w) {
			return
		}
		if prev.Kind != model.KindEntries && !h.entriesNameRoom(w, r, siteID, name) {
			return
		}
		if !h.declareLocked(w, r, siteID, name, model.KindEntries, private, one, notify, req.ConfirmPublic, "public Submissions") {
			return
		}
		msg := "Submissions, private: signed-in visitors on https://" + home.Host + " add entries and see, change or withdraw their own; only you read them all."
		if !private {
			msg = "Submissions, public: signed-in visitors add entries and change or withdraw their own; anyone can read the list (who sent each stays visible only to you)."
		}
		if one {
			msg += " One entry per person."
		}
		msg += " " + notifyWords(notify)
		writeJSON(w, http.StatusOK, map[string]any{
			"site": siteName, "name": name, "kind": model.KindEntries, "label": kindLabel(model.KindEntries),
			"visibility": visibilityWord(private), "one_per_person": one, "notify": notify, "message": msg,
		})
	case model.KindPersonal, model.KindBoard:
		h.declarePersonalOrBoard(w, r, siteID, siteName, name, req.Kind, prev, req.Visibility, req.OnePerPerson, req.Notify, req.ConfirmPublic)
	default:
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: `kind is "entries" (Submissions: things visitors send), "content" (Page info: only the owner writes it), "mine" (Personal: one private record per signed-in visitor) or "board" (Shared board: a list signed-in visitors edit together)`, Code: "invalid_kind"})
	}
}

// declareLocked writes the declaration under the name's lock, where the
// checks that depend on what the name holds run (a save may land at any
// moment): a change to or from Personal finds the name empty, and a private
// name made readable by anyone (as what) holds nothing, or the owner
// confirmed. Writes the refusal; false then.
func (h *SiteHandler) declareLocked(w http.ResponseWriter, r *http.Request, siteID, name, kind string, private, one bool, notify string, confirmPublic bool, what string) bool {
	err := h.legacySvc().DeclareDataLocked(r.Context(), siteID, name, kind, private, one, notify, confirmPublic)
	var pe *model.PrivateItemsError
	switch {
	case errors.Is(err, model.ErrNameHasRows) && kind == model.KindPersonal:
		writeHoldsData(w, name)
	case errors.Is(err, model.ErrNameHasRows):
		writeHasRecords(w, name)
	case errors.Is(err, model.ErrSeveralItems):
		writeJSON(w, http.StatusConflict, errorResponse{Error: fmt.Sprintf("%q holds several entries, and page info is one document; use another name, or clear the list first", name), Code: "has_entries"})
	case errors.As(err, &pe):
		writePrivateItems(w, name, pe, what)
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
	default:
		return true
	}
	return false
}

// writePrivateItems: a private name that holds entries (live or in Recently
// deleted, which a restore brings back) never becomes page info, and becomes
// readable by anyone (as what) only when the owner confirms, knowing what
// that shows. The 409 has_entries or confirm_public, with the count.
func writePrivateItems(w http.ResponseWriter, name string, e *model.PrivateItemsError, what string) {
	n := e.Live + e.Deleted
	noun := "entries"
	if n == 1 {
		noun = "entry"
	}
	held := fmt.Sprintf("%d private %s", n, noun)
	if e.Deleted > 0 {
		held += fmt.Sprintf(" (%d in Recently deleted, which a restore brings back)", e.Deleted)
	}
	if e.Content {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": fmt.Sprintf("%q is private and holds %s that only you can read; page info is public, so a private name becomes page info only when it is empty. Use another name, or delete its entries and empty its Recently deleted first", name, held),
			"code":  "has_entries", "count": n,
		})
		return
	}
	writeJSON(w, http.StatusConflict, map[string]any{
		"error": fmt.Sprintf("%q holds %s that only you can read now. As %s, anyone who can open the site reads them: every field of each entry (who sent each stays visible only to you). "+
			"To go ahead, send the same request with \"confirm_public\": true; or use another name", name, held, what),
		"code": "confirm_public", "count": n,
	})
}

// writeHasRecords: a Personal name that holds records stays Personal.
func writeHasRecords(w http.ResponseWriter, name string) {
	writeJSON(w, http.StatusConflict, errorResponse{
		Error: fmt.Sprintf("%q is Personal and holds records that belong to the people who saved them, so it never becomes another kind while it holds any. Use another name, or empty it and delete what is in its Recently deleted for good first", name),
		Code:  "has_records",
	})
}

// writeHoldsData: a name that holds data never becomes Personal.
func writeHoldsData(w http.ResponseWriter, name string) {
	writeJSON(w, http.StatusConflict, errorResponse{
		Error: fmt.Sprintf("%q already holds data (or has items in Recently deleted), and Personal records are private to each person, so it would disappear from your view. Use another name, or empty it and delete what is in its Recently deleted for good first", name),
		Code:  "has_entries",
	})
}

func validKind(k string) bool {
	switch k {
	case model.KindContent, model.KindEntries, model.KindPersonal, model.KindBoard:
		return true
	}
	return false
}

// declarePersonalOrBoard declares name Personal (mine) or Shared board
// (board). Neither takes options. Both need the site on an address of its
// own, where visitors sign in. A name that already holds data never becomes
// Personal (it would vanish from the owner's view); private entries become a
// (public) board only with confirm_public.
func (h *SiteHandler) declarePersonalOrBoard(w http.ResponseWriter, r *http.Request, siteID, siteName, name, kind string, prev model.DataSettings, visibility string, one *bool, notify string, confirmPublic bool) {
	if (visibility != "" && !(kind == model.KindBoard && visibility == "public")) || (one != nil && *one) || (notify != "" && notify != model.NotifyOff) {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "Personal and Shared board take no options: visibility, one_per_person and notify apply to Submissions (kind entries)", Code: "invalid_kind"})
		return
	}
	internal := func() { writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"}) }
	if prev.Kind != kind {
		if kind == model.KindPersonal {
			has, err := h.legacySvc().NameHasRows(r.Context(), siteID, name)
			if err != nil {
				internal()
				return
			}
			if has {
				writeHoldsData(w, name)
				return
			}
		}
		max, label := h.savedData.PersonalNamesMax, "Personal"
		if kind == model.KindBoard {
			max, label = h.savedData.BoardNamesMax, "Shared board"
		}
		n, err := h.legacySvc().CountKindNames(r.Context(), siteID, kind, name)
		if err != nil {
			internal()
			return
		}
		if n >= max {
			writeJSON(w, http.StatusConflict, errorResponse{Error: fmt.Sprintf("a site has at most %d %s names", max, label), Code: "too_many_names"})
			return
		}
	}
	home, hasHome, err := h.siteHomeFor(r.Context(), siteID)
	if err != nil {
		internal()
		return
	}
	if !hasHome {
		writeJSON(w, http.StatusConflict, errorResponse{
			Error: "visitors save here while signed in, and visitors sign in on the site's own address: connect one first (a free <name>." + h.handoutBase() + " address works, or your own domain)",
			Code:  "custom_domain_required",
		})
		return
	}
	if !h.declareLocked(w, r, siteID, name, kind, false, false, model.NotifyOff, confirmPublic, "a Shared board") {
		return
	}
	msg := fmt.Sprintf("Personal: each visitor signed in on https://%s keeps one record here (at most %d KB), on any device; only they change it. You see how many people have one (from 3 people up), never what they saved: Simple Host's owner tools never show a person's Personal record; the site's own pages run in the visitor's browser and can read that visitor's record, so only use Personal on sites you trust. Never publish a page that sends what it reads from a Personal record anywhere else.", home.Host, h.savedData.PersonalMaxKB)
	if kind == model.KindBoard {
		msg = fmt.Sprintf("Shared board: anyone who can open the site reads it; visitors signed in on https://%s add items and change or delete any item, one at a time (at most %d items of %d KB). Only you can clear it; changed and deleted items can be restored for %d days.", home.Host, h.savedData.BoardMax, h.savedData.BoardItemMaxKB, h.savedData.UndoDays)
	}
	writeJSON(w, http.StatusOK, map[string]any{"site": siteName, "name": name, "kind": kind, "label": kindLabel(kind), "message": msg})
}

// publicEntriesOK: public Submissions take entries only from visitors signed
// in, and with WRITE_AUTH_MODE=off this install reads no visitor sign-in on
// public writes, so they could never take one. Writes the 409 when so.
// (Private Submissions have their own sign-in check and work in every mode.)
func (h *SiteHandler) publicEntriesOK(w http.ResponseWriter) bool {
	if h.writeAuthMode != "off" {
		return true
	}
	writeJSON(w, http.StatusConflict, errorResponse{
		Error: "public Submissions need visitors to sign in before they save, and visitor sign-in for public saves is off on this install (WRITE_AUTH_MODE=off). Keep them private (only you read them), or leave the name Shared",
		Code:  "visitor_sign_in_off",
	})
	return false
}

// entriesNameRoom: the site may declare one more Submissions name
// (SAVED_DATA_ENTRIES_NAMES_MAX). Writes the refusal.
func (h *SiteHandler) entriesNameRoom(w http.ResponseWriter, r *http.Request, siteID, name string) bool {
	n, err := h.legacySvc().CountKindNames(r.Context(), siteID, model.KindEntries, name)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return false
	}
	if n >= h.savedData.EntriesNamesMax {
		writeJSON(w, http.StatusConflict, errorResponse{Error: fmt.Sprintf("a site has at most %d Submissions names; keep one name per kind of thing visitors send", h.savedData.EntriesNamesMax), Code: "too_many_names"})
		return false
	}
	return true
}

// normalizeKind accepts the product words too ("Page info", "submissions").
func normalizeKind(k string) string {
	switch strings.ToLower(strings.TrimSpace(strings.NewReplacer("_", " ", "-", " ").Replace(k))) {
	case "content", "page info":
		return model.KindContent
	case "entries", "submissions":
		return model.KindEntries
	case "mine", "personal":
		return model.KindPersonal
	case "board", "shared board":
		return model.KindBoard
	}
	return strings.TrimSpace(k)
}

func notifyWords(notify string) string {
	switch notify {
	case model.NotifyEach:
		return "You get an email soon after new entries arrive (batched)."
	case model.NotifyDaily:
		return "You get a daily email when there are new entries."
	}
	return "No email about new entries."
}

// dataSummary is one name in the owner's list.
type dataSummary struct {
	model.CollectionSummary
	Label string `json:"label"`
}

// listData is GET /v1/sites/{s}/data: every data name with its kind and
// settings, who may save, and whether undeclared names take saves.
func (h *SiteHandler) listData(w http.ResponseWriter, r *http.Request) {
	_, siteID, siteName, _, ok := h.ownedDataSite(w, r)
	if !ok {
		return
	}
	summaries, err := h.legacySvc().ListCollectionSummariesByID(r.Context(), siteID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	savers, err := h.legacySvc().GetSavers(r.Context(), siteID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	set, ok := h.dataSettings(w, r, siteID, "")
	if !ok {
		return
	}
	names := make([]dataSummary, 0, len(summaries))
	for _, s := range summaries {
		names = append(names, dataSummary{CollectionSummary: s, Label: nameLabel(s.Kind, s.Private, set.Shared)})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"site": siteName, "names": names, "savers": savers,
		"undeclared_names_take_saves": set.Shared,
		"limits": map[string]int{
			"content_max_kb": h.savedData.ContentMaxKB, "entry_max_kb": h.savedData.EntryMaxKB,
			"entries_max": h.savedData.EntriesMax, "content_names_max": h.savedData.ContentNamesMax,
			"withdraw_undo_minutes": h.savedData.WithdrawUndoMinutes,
			"personal_max_kb":       h.savedData.PersonalMaxKB, "board_item_max_kb": h.savedData.BoardItemMaxKB,
			"board_max": h.savedData.BoardMax,
		},
	})
}

// ---- who may save -------------------------------------------------------------------

var saverDomain = regexp.MustCompile(`^@?([a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+)$`)

// normalizeSaver turns "Ann@Example.com", "@example.com" or "example.com"
// into "ann@example.com" or "@example.com". ok=false for anything else.
func normalizeSaver(p string) (string, bool) {
	p = strings.ToLower(strings.TrimSpace(p))
	if p == "" || len(p) > 254 {
		return "", false
	}
	if at := strings.LastIndex(p, "@"); at > 0 {
		local, domain := p[:at], p[at+1:]
		if strings.ContainsAny(local, " \t\r\n<>,;") || !saverDomain.MatchString(domain) {
			return "", false
		}
		return local + "@" + domain, true
	}
	if m := saverDomain.FindStringSubmatch(p); m != nil {
		return "@" + m[1], true
	}
	return "", false
}

func normalizeSavers(in []string) ([]string, string) {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, p := range in {
		n, ok := normalizeSaver(p)
		if !ok {
			return nil, p
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out, ""
}

// getSavers is GET /v1/sites/{s}/savers.
func (h *SiteHandler) getSavers(w http.ResponseWriter, r *http.Request) {
	_, siteID, siteName, _, ok := h.ownedDataSite(w, r)
	if !ok {
		return
	}
	s, err := h.legacySvc().GetSavers(r.Context(), siteID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"site": siteName, "mode": s.Mode, "allow": s.Allow, "block": s.Block})
}

// putSavers is PUT /v1/sites/{s}/savers {"mode": "anyone"|"listed", "allow":
// [...], "block": [...]}: replaces who may save. Entries are emails or whole
// domains ("@company.com"). The owner always may.
func (h *SiteHandler) putSavers(w http.ResponseWriter, r *http.Request) {
	_, siteID, siteName, _, ok := h.ownedDataSite(w, r)
	if !ok || h.refuseSuspendedSiteID(w, r, siteID) {
		return
	}
	var req struct {
		Mode  string   `json:"mode"`
		Allow []string `json:"allow"`
		Block []string `json:"block"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: `send {"mode": "anyone"} or {"mode": "listed", "allow": ["ann@example.com", "@company.com"]}, with an optional "block" list`, Code: "invalid_savers"})
		return
	}
	switch req.Mode {
	case "", model.SaversAnyone:
		req.Mode = model.SaversAnyone
	case model.SaversListed:
	default:
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: `mode is "anyone" (anyone who signs in) or "listed" (only the emails and domains in allow)`, Code: "invalid_savers"})
		return
	}
	allow, bad := normalizeSavers(req.Allow)
	if bad == "" {
		var badBlock string
		req.Block, badBlock = normalizeSavers(req.Block)
		bad = badBlock
	}
	if bad != "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: fmt.Sprintf("%q is not an email address or a domain (write a domain as @company.com)", bad), Code: "invalid_savers"})
		return
	}
	if len(allow)+len(req.Block) > h.savedData.SaversMax {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: fmt.Sprintf("at most %d emails and domains in total", h.savedData.SaversMax), Code: "too_many_savers"})
		return
	}
	s := model.Savers{Mode: req.Mode, Allow: allow, Block: req.Block}
	if err := h.legacySvc().SetSavers(r.Context(), legacy.SetSaversInput{SiteID: siteID, Savers: s}); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"site": siteName, "mode": s.Mode, "allow": s.Allow, "block": s.Block, "message": saversWords(s)})
}

func saversWords(s model.Savers) string {
	msg := "Anyone who signs in can save on this site."
	if s.Mode == model.SaversListed {
		msg = fmt.Sprintf("Only the %d emails and domains you listed can save on this site (you always can).", len(s.Allow))
	}
	if len(s.Block) > 0 {
		msg += fmt.Sprintf(" %d blocked.", len(s.Block))
	}
	return msg
}

// blockSaver is POST /v1/sites/{s}/savers/block {"email": "..."} or
// {"collection": "<name>", "id": <entry id>} (block whoever sent that entry):
// adds them to the site's block list. Their entries stay; the owner deletes
// those separately.
func (h *SiteHandler) blockSaver(w http.ResponseWriter, r *http.Request) {
	_, siteID, siteName, _, ok := h.ownedDataSite(w, r)
	if !ok || h.refuseSuspendedSiteID(w, r, siteID) {
		return
	}
	var req struct {
		Email      string          `json:"email"`
		Collection string          `json:"collection"`
		ID         json.RawMessage `json:"id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: `send {"email": "..."} or {"collection": "rsvps", "id": 12}`, Code: "invalid_savers"})
		return
	}
	pattern := req.Email
	if pattern == "" && req.Collection != "" {
		id, err := strconv.ParseInt(strings.Trim(string(req.ID), `" `), 10, 64)
		if err != nil || id <= 0 || !validCollectionName.MatchString(req.Collection) {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "collection and id name one entry (the id from the list)", Code: "invalid_savers"})
			return
		}
		// Who keeps a Personal record is theirs too: never revealed.
		if h.refusePersonal(w, r, siteID, req.Collection) {
			return
		}
		author, err := h.legacySvc().GetItemAuthor(r.Context(), siteID, req.Collection, id)
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "no such entry", Code: "not_found"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
			return
		}
		if author.Email == "" || author.Email == model.OperatorLabel {
			writeJSON(w, http.StatusConflict, errorResponse{Error: "that entry was saved without a sign-in, so there is nobody to block", Code: "no_author"})
			return
		}
		// The address the server stamped, without a +tag, so the same
		// mailbox under another tag is blocked too.
		pattern = model.BaseEmail(author.Email)
	}
	norm, valid := normalizeSaver(pattern)
	if !valid {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "send an email address, a domain (@company.com), or an entry (collection and id)", Code: "invalid_savers"})
		return
	}
	cur, err := h.legacySvc().GetSavers(r.Context(), siteID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if !slices.Contains(cur.Block, norm) && len(cur.Allow)+len(cur.Block) >= h.savedData.SaversMax {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: fmt.Sprintf("at most %d emails and domains in total", h.savedData.SaversMax), Code: "too_many_savers"})
		return
	}
	n, err := h.legacySvc().AddBlocked(r.Context(), siteID, norm)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"site": siteName, "blocked": norm, "block_count": n,
		"message": norm + " can no longer save on this site. What they already sent stays until you delete it."})
}

// ---- email on new submissions ----------------------------------------------------------

const notifyStopDomain = "simple-host submissions email stop v1"

// notifyMAC signs a stop link. The key is derived from the admin API key (it
// lasts across restarts, unlike the export-link key), never used as is.
func (h *SiteHandler) notifyMAC(payload string) []byte {
	k := sha256.Sum256([]byte(notifyStopDomain + "\x00" + h.adminAPIKey))
	mac := hmac.New(sha256.New, k[:])
	mac.Write([]byte(payload))
	return mac.Sum(nil)
}

func (h *SiteHandler) notifyStopToken(siteID, name string) string {
	payload := siteID + "/" + name
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + base64.RawURLEncoding.EncodeToString(h.notifyMAC(payload))
}

func (h *SiteHandler) checkNotifyStopToken(tok string) (siteID, name string, ok bool) {
	enc, sig, found := strings.Cut(tok, ".")
	if !found || h.adminAPIKey == "" {
		return "", "", false
	}
	raw, err1 := base64.RawURLEncoding.DecodeString(enc)
	got, err2 := base64.RawURLEncoding.DecodeString(sig)
	if err1 != nil || err2 != nil || !hmac.Equal(got, h.notifyMAC(string(raw))) {
		return "", "", false
	}
	siteID, name, found = strings.Cut(string(raw), "/")
	return siteID, name, found
}

// StartSubmissionEmails emails owners about new Submissions every
// SAVED_DATA_NOTIFY_EACH_MINUTES: at most one email per name per that
// interval ("each") or per SAVED_DATA_NOTIFY_DAILY_HOURS ("daily"), counting
// what arrived since the last one.
type submissionEmailOwner struct {
	h       *SiteHandler
	runner  *workers.Runner
	tracker *workers.Tracker
	once    sync.Once
}

func (h *SiteHandler) SubmissionEmailOwner() workers.Owner       { return h.notifyEngine().submissions }
func (h *SiteHandler) StartSubmissionEmails(ctx context.Context) { h.SubmissionEmailOwner().Start(ctx) }
func (o *submissionEmailOwner) Stop()                            { o.runner.Stop() }
func (o *submissionEmailOwner) Wait()                            { o.runner.Wait() }
func (o *submissionEmailOwner) Status() []workers.Status         { return o.runner.Status() }
func (o *submissionEmailOwner) Start(ctx context.Context) {
	o.once.Do(func() {
		o.runner.Go(ctx, func(ctx context.Context) {
			t := time.NewTicker(time.Duration(o.h.savedData.NotifyEachMinutes) * time.Minute)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					o.h.sendSubmissionEmails(ctx)
				}
			}
		})
	})
}

func (h *SiteHandler) sendSubmissionEmails(ctx context.Context) int {
	svc := h.storeMail()
	svc.Tracker = h.notifyEngine().submissions.tracker
	return svc.SendSubmissions(ctx)
}

// notifyStop is GET/POST /v1/data-notify/stop?t=: the emailed "stop these"
// link. GET shows a button (mail scanners follow links); POST turns the
// name's email off.
func (h *SiteHandler) notifyStop(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if fragmentLinkGET(h.pipeline(), w, r, h.chromeBase(r), "/v1/data-notify/stop") {
		return
	}
	tok := linkToken(w, r)
	siteID, name, ok := h.checkNotifyStopToken(tok)
	if !ok {
		h.renderMessagePage(w, r, http.StatusNotFound, "This link does not work",
			"Sign in to Simple Host and change the email setting from your sites page.", h.exportLinkBase()+"/", "Go to Simple Host")
		return
	}
	if linkConfirming(r) {
		h.pipeline().WriteMessagePage(w, r, h.chromeBase(r), http.StatusOK, "Stop emails about "+html.EscapeString(name)+"?",
			"You will not be emailed about new entries in "+html.EscapeString(name)+" any more. You can turn it back on from your sites page.",
			"", "", confirmForm("/v1/data-notify/stop", map[string]string{"t": tok}, "Stop these emails"))
		return
	}
	if _, err := h.legacySvc().SetNotify(r.Context(), siteID, name, model.NotifyOff); err != nil {
		h.renderServiceError(w, r)
		return
	}
	h.renderMessagePage(w, r, http.StatusOK, "Stopped: no more emails about "+html.EscapeString(name),
		"Turn them back on any time from your sites page.", h.exportLinkBase()+"/", "Go to Simple Host")
}

// boardWriter is who may add to, change or delete from a board: the owner
// (key, connector, or signed in on the site, as for Page info), or a visitor
// signed in on the site's own address with the CSRF header who is allowed to
// save here, within the per-address write rate. Writes the refusal.
func (h *SiteHandler) boardWriter(w http.ResponseWriter, r *http.Request, siteID string) (model.Actor, bool) {
	result, err := h.legacyBoardWriterSvc(r).BoardWriter(r.Context(), legacy.BoardWriterInput{SiteID: siteID, Key: r.Header.Get("X-API-Key"), Preview: previewReferer(r), ContentHost: strings.EqualFold(requestHostName(r), h.contentHost), CSRFValid: r.Header.Get(visitorCSRFHeader) == visitorCSRFValue, VisitorPass: r.Header.Get(visitorPassHeader) != ""})
	for k, v := range result.Headers {
		w.Header()[k] = v
	}
	if err == nil {
		return result.Actor, true
	}
	var opErr *legacy.Error
	if errors.As(err, &opErr) {
		switch opErr.Code {
		case "site_not_found":
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
		case "invalid_api_key":
			writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "invalid API key", Code: "invalid_api_key"})
		case "owner_only":
			writeJSON(w, http.StatusForbidden, errorResponse{Error: "only the site owner can change page info", Code: "owner_only"})
		case "csrf_required":
			writeJSON(w, http.StatusForbidden, errorResponse{Error: "missing CSRF header", Code: "csrf_required"})
		case "site_offline":
			writeSiteOffline(w)
		case "preview_read_only":
			writePreviewReadOnly(w)
		case "visitor_auth_required":
			writeVisitorAuthRequired(w)
		case "account_suspended":
			writeAccountSuspended(w)
		case "not_allowed_to_save":
			writeNotAllowedToSave(w)
		case "rate_limited":
			tooManyRequests(w)
		default:
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		}
	} else {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
	}
	return model.Actor{}, false
}
