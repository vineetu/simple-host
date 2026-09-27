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
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/config"
	db "github.com/vsriram/simple-host/internal/db"
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
// AI create and uploaded pages keep working. Page info and Submissions are
// upgrades the owner's agent declares. SAVED_DATA_DEFAULT_KIND=declare_first
// makes an undeclared name take no saves at all (409 declare_first) on sites
// made after the kinds; sites made before them (sites.legacy_data) stay
// Shared either way.

var kindLabels = map[string]string{db.KindContent: "Page info", db.KindEntries: "Submissions", db.KindPersonal: "Personal", db.KindBoard: "Shared board"}

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
			"or {\"kind\": \"content\"} for page info only the owner writes (menu, schedule, prices); with the connector, declare_data", name, siteName, name),
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
func (h *SiteHandler) dataSettings(w http.ResponseWriter, r *http.Request, siteID, name string) (db.DataSettings, bool) {
	set, err := db.GetDataSettings(r.Context(), h.database, siteID, name)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
		return set, false
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return set, false
	}
	if h.savedData.DefaultKind != config.DefaultKindDeclareFirst {
		set.Shared = true
	}
	return set, true
}

// entriesLike: the name holds Submissions, declared or (on a legacy site)
// an undeclared list that behaves as before.
func entriesLike(set db.DataSettings) bool {
	return set.Kind == db.KindEntries || (set.Kind == "" && set.Shared)
}

// listLike: the name is a list of items anyone may be shown (Submissions,
// Shared) or a Shared board.
func listLike(set db.DataSettings) bool {
	return entriesLike(set) || set.Kind == db.KindBoard
}

// appendAllowed is the kind check before any item is added to a name. Writes
// the refusal.
func (h *SiteHandler) appendAllowed(w http.ResponseWriter, set db.DataSettings, siteName string) bool {
	switch {
	case set.Kind == db.KindContent:
		writeWrongKind(w, set.Name, set.Kind, "only the owner saves it, as a whole document with PUT /v1/sites/"+siteName+"/data/"+set.Name)
		return false
	case set.Kind == db.KindPersonal:
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
func (h *SiteHandler) entryMaxBytes(set db.DataSettings) int64 {
	if set.Kind == db.KindEntries {
		return int64(h.savedData.EntryMaxKB) << 10
	}
	return maxCollectionItemSize
}

// saveEntry stores one new item in a declared Submissions name with its rules
// (one per person, the per-name cap), or appends to a legacy list. Writes the
// answer on a refusal; ok=false then.
func (h *SiteHandler) saveEntry(w http.ResponseWriter, r *http.Request, siteID string, set db.DataSettings, body []byte, a db.Actor) (db.CollectionItem, bool) {
	var item db.CollectionItem
	var err error
	var have int64
	if set.Kind == db.KindEntries {
		item, have, err = db.AppendEntry(r.Context(), h.database, siteID, set.Name, body, a, set.OnePerPerson && a.ID != "", h.savedData.EntriesMax)
	} else {
		item, err = db.AppendCollectionItemByID(r.Context(), h.database, siteID, set.Name, body, a)
	}
	switch {
	case errors.Is(err, db.ErrOnePerPerson) && have == 0:
		writeJSON(w, http.StatusConflict, errorResponse{
			Error: "this address already has an entry here (one per person), sent from another sign-in; change or withdraw it from there",
			Code:  "one_per_person",
		})
	case errors.Is(err, db.ErrOnePerPerson):
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": "you already have an entry here (one per person): change it with PATCH .../items/" + strconv.FormatInt(have, 10) + " or withdraw it first",
			"code":  "one_per_person", "id": have,
		})
	case errors.Is(err, db.ErrNameFull):
		writeJSON(w, http.StatusConflict, errorResponse{
			Error: fmt.Sprintf("this list is full (%d entries); the owner can delete entries or clear it to make room", h.savedData.EntriesMax),
			Code:  "list_full",
		})
	case errors.Is(err, sql.ErrNoRows):
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
	default:
		return item, true
	}
	return item, false
}

// saverOK applies the site's who-may-save setting to a visitor write. email
// is the visitor's verified address ("" when nobody is signed in). Writes the
// refusal.
func (h *SiteHandler) saverOK(w http.ResponseWriter, r *http.Request, siteID, email string) bool {
	ok, err := db.SaverAllowed(r.Context(), h.database, siteID, email)
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
// as an address nobody blocked. The site's owner always may. Writes the
// refusal.
func (h *SiteHandler) personSaverOK(w http.ResponseWriter, r *http.Request, siteID, userID string) bool {
	email := ""
	if userID != "" {
		var err error
		if email, err = visitorEmail(r.Context(), h.database, userID); err != nil || email == "" {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
			return false
		}
	}
	ok, err := db.SaverAllowed(r.Context(), h.database, siteID, email)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return false
	}
	if ok {
		return true
	}
	if userID != "" {
		if ownerID, _, err := db.GetSiteWriteGate(r.Context(), h.database, siteID); err == nil && ownerID == userID {
			return true
		}
	}
	writeNotAllowedToSave(w)
	return false
}

// visitorWriteOK is the gate every page write passes (visitorWriteGate), then
// the site's who-may-save setting for anyone writing without the owner's key.
func (h *SiteHandler) visitorWriteOK(w http.ResponseWriter, r *http.Request, siteID, siteName, route, collection string) (db.Actor, bool) {
	a, ok := h.visitorWriteGate(w, r, siteID, siteName, route, collection)
	if !ok || !isVisitorActor(a) {
		return a, ok
	}
	if !h.personSaverOK(w, r, siteID, a.ID) {
		return a, false
	}
	return a, true
}

// ---- the page-facing routes -----------------------------------------------------

// readSite resolves a page-facing read: the owner's key names the site
// exactly; a request naming a page passes the Origin gate; a request naming
// none (curl, an agent) is served as public reads are. ownerKey reports the
// first. Applies the read limit (unless the caller hands the read to a
// handler that counts it itself) and, for anyone but the owner, taken-down
// and offline. Writes the answer on failure.
func (h *SiteHandler) readSite(w http.ResponseWriter, r *http.Request, siteName, name string, countRead bool) (siteID string, ownerKey, ok bool) {
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
	siteID, _, ok := h.readSite(w, r, siteName, name, true)
	if !ok {
		return
	}
	set, ok := h.dataSettings(w, r, siteID, name)
	if !ok {
		return
	}
	resp := map[string]any{"name": name, "declared": set.Kind != "", "kind": nil, "label": nameLabel(set.Kind, set.Private, set.Shared)}
	if set.Kind != "" {
		resp["kind"] = set.Kind
	}
	if entriesLike(set) {
		resp["visibility"] = visibilityWord(set.Private)
		resp["one_per_person"] = set.OnePerPerson && set.Kind == db.KindEntries
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
	siteID, ownerKey, ok := h.readSite(w, r, siteName, name, false)
	if !ok {
		return
	}
	set, ok := h.dataSettings(w, r, siteID, name)
	if !ok {
		return
	}
	if (set.Kind == db.KindContent || !listLike(set) || q.Get("count") != "") && !h.allowRead(w, r, siteID) {
		return
	}
	switch {
	case set.Kind == db.KindPersonal:
		h.getPersonal(w, r, siteID, name, ownerKey)
	case set.Kind == db.KindContent:
		doc, found, err := db.GetContent(r.Context(), h.database, siteID, name)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
			return
		}
		resp := map[string]any{"name": name, "kind": db.KindContent, "data": nil}
		if found {
			// The server's own stamp of who sent an entry is the owner's to
			// read, whatever the document once was.
			if ownerKey || h.ownerBrowserView(r, siteID) {
				w.Header().Set("Cache-Control", "private, no-store")
				resp["data"] = doc.Data
			} else {
				resp["data"] = withoutSubmitter(doc.Data)
			}
			resp["saved_at"] = doc.CreatedAt
		}
		writeJSON(w, http.StatusOK, resp)
	case !listLike(set):
		writeDeclareFirst(w, siteName, name)
	case q.Get("count") != "":
		if set.Private && !ownerKey && !h.ownerBrowserView(r, siteID) {
			w.Header().Set("Cache-Control", "private, no-store")
			writePrivateNotFound(w)
			return
		}
		n, err := db.CountLiveItems(r.Context(), h.database, siteID, name)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"name": name, "count": n})
	default:
		h.listCollection(w, r)
	}
}

// visitorOnSite is the visitor signed in to siteID on its own address, from a
// page there (the __Host- cookie, same origin), with the CSRF header when
// needCSRF. Writes 401/403 and ok=false otherwise.
func (h *SiteHandler) visitorOnSite(w http.ResponseWriter, r *http.Request, siteID string, needCSRF bool) (db.VisitorSession, bool) {
	if strings.EqualFold(requestHostName(r), h.contentHost) {
		writeVisitorAuthRequired(w)
		return db.VisitorSession{}, false
	}
	sess, ok := h.strictVisitorSession(r, siteID)
	if !ok {
		writeVisitorAuthRequired(w)
		return sess, false
	}
	if susp, err := db.UserSuspended(r.Context(), h.database, sess.UserID); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return sess, false
	} else if susp {
		writeAccountSuspended(w)
		return sess, false
	}
	if needCSRF && r.Header.Get(visitorCSRFHeader) != visitorCSRFValue {
		writeJSON(w, http.StatusForbidden, errorResponse{Error: "missing CSRF header", Code: "csrf_required"})
		return sess, false
	}
	_ = db.TouchVisitorSession(r.Context(), h.database, sess.ID)
	return sess, true
}

// listMine is GET .../data/{name}?mine=1: the signed-in visitor's own live
// entries, newest first (?limit, ?before as for lists).
func (h *SiteHandler) listMine(w http.ResponseWriter, r *http.Request, siteName, name string) {
	w.Header().Set("Cache-Control", "private, no-store")
	if siteName == "" || !validCollectionName.MatchString(name) {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid site or data name"})
		return
	}
	siteID, err := h.resolveSiteID(r, siteName)
	if err != nil {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
		return
	}
	if !h.allowRead(w, r, siteID) || h.refuseSuspendedSiteID(w, r, siteID) {
		return
	}
	sess, ok := h.visitorOnSite(w, r, siteID, false)
	if !ok {
		return
	}
	set, ok := h.dataSettings(w, r, siteID, name)
	if !ok {
		return
	}
	if !listLike(set) {
		if set.Kind == db.KindContent || set.Kind == db.KindPersonal {
			writeWrongKind(w, name, set.Kind, "read it with GET /v1/sites/"+siteName+"/data/"+name)
			return
		}
		writeDeclareFirst(w, siteName, name)
		return
	}
	limit, before := pageArgs(r)
	items, err := db.ListOwnEntries(r.Context(), h.database, siteID, name, sess.UserID, limit, before)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	var next *int64
	if len(items) == limit && limit > 0 {
		n := items[len(items)-1].ID
		next = &n
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "next": next, "mine": true})
}

// isOwnerRequest: the request carries a key (the item routes send those to
// the owner's handlers, which check it), or comes from the site's owner
// signed in on the site.
func (h *SiteHandler) isOwnerRequest(r *http.Request, siteID string) bool {
	return r.Header.Get("X-API-Key") != "" || h.ownerBrowserView(r, siteID)
}

// putContent is PUT /v1/sites/{s}/data/{name}: the owner replaces a Page info
// document (one JSON object, at most SAVED_DATA_CONTENT_MAX_KB). The earlier
// document goes to the name's history.
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
	if h.refuseSuspendedSiteID(w, r, siteID) {
		return
	}
	set, ok := h.dataSettings(w, r, siteID, name)
	if !ok {
		return
	}
	switch set.Kind {
	case db.KindContent:
	case db.KindPersonal:
		h.putPersonal(w, r, siteID, name)
		return
	case db.KindEntries, db.KindBoard:
		writeWrongKind(w, name, set.Kind, "add items with POST /v1/sites/"+siteName+"/data/"+name+"; only the owner empties it, with DELETE /v1/sites/"+siteName+"/collections/"+name)
		return
	default:
		writeJSON(w, http.StatusConflict, errorResponse{
			Error: fmt.Sprintf("%q is not page info yet: declare it first with PUT /v1/sites/%s/data/%s/kind and {\"kind\": \"content\"} (connector: declare_data), then save it here", name, siteName, name),
			Code:  "declare_first",
		})
		return
	}
	actor, ok := h.contentWriter(w, r, siteID)
	if !ok {
		return
	}
	maxBytes := int64(h.savedData.ContentMaxKB) << 10
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBytes))
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: fmt.Sprintf("page info is at most %d KB", h.savedData.ContentMaxKB), Code: "item_too_large"})
			return
		}
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}
	var obj map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&obj); err != nil || obj == nil || dec.More() {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "page info is one JSON object, e.g. {\"items\": [...]}", Code: "not_an_object"})
		return
	}
	doc, err := db.PutContent(r.Context(), h.database, siteID, name, json.RawMessage(bytes.TrimSpace(raw)), actor, h.siteMaxBytes())
	h.boundHistory(r, siteID)
	switch {
	case errors.Is(err, db.ErrSiteFull):
		h.writeSiteFull(w)
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
	default:
		writeJSON(w, http.StatusOK, map[string]any{"name": name, "kind": db.KindContent, "data": doc.Data, "saved_at": doc.CreatedAt})
	}
}

// contentWriter is who may write Page info: the owner's (or the admin's) key
// or connector, or the owner signed in on the site's own address with the
// CSRF header. A key of another account gets the 404 of a missing site;
// anyone else 403 owner_only. Writes the refusal.
func (h *SiteHandler) contentWriter(w http.ResponseWriter, r *http.Request, siteID string) (db.Actor, bool) {
	ownerID, _, err := db.GetSiteWriteGate(r.Context(), h.database, siteID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
		return db.Actor{}, false
	}
	if key := r.Header.Get("X-API-Key"); key != "" {
		u, ok, err := h.resolveWriterKey(r.Context(), key)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
			return db.Actor{}, false
		}
		if !ok {
			writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "invalid API key", Code: "invalid_api_key"})
			return db.Actor{}, false
		}
		if !u.IsAdmin && u.ID != ownerID {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return db.Actor{}, false
		}
		a := db.Actor{ID: u.ID, Kind: actorOwner}
		if u.ID != ownerID {
			a.Kind = actorAdmin
		}
		return h.withAuthorEmail(r.Context(), a), true
	}
	if h.ownerBrowserView(r, siteID) {
		if r.Header.Get(visitorCSRFHeader) != visitorCSRFValue {
			writeJSON(w, http.StatusForbidden, errorResponse{Error: "missing CSRF header", Code: "csrf_required"})
			return db.Actor{}, false
		}
		return h.withAuthorEmail(r.Context(), db.Actor{ID: ownerID, Kind: actorOwner}), true
	}
	writeJSON(w, http.StatusForbidden, errorResponse{Error: "only the site owner can change page info", Code: "owner_only"})
	return db.Actor{}, false
}

// ownEntry resolves {sitename}/{coll}/{id} for a visitor acting on their own
// entry: the visitor signed in on the site (with CSRF), a Submissions name,
// and a live item they sent. Anything else is 404 not_found (nothing about
// other people's entries is revealed).
func (h *SiteHandler) ownEntry(w http.ResponseWriter, r *http.Request, siteID, siteName string, live bool) (db.VisitorSession, db.DataSettings, int64, bool) {
	name := strings.TrimSpace(r.PathValue("coll"))
	sess, ok := h.visitorOnSite(w, r, siteID, true)
	if !ok {
		return sess, db.DataSettings{}, 0, false
	}
	set, ok := h.dataSettings(w, r, siteID, name)
	if !ok {
		return sess, set, 0, false
	}
	if !entriesLike(set) {
		if set.Kind == db.KindContent {
			writeWrongKind(w, name, set.Kind, "only the owner changes it")
		} else {
			writeDeclareFirst(w, siteName, name)
		}
		return sess, set, 0, false
	}
	id, valid := pathID(r, "id")
	if !valid {
		writePrivateNotFound(w)
		return sess, set, 0, false
	}
	author, err := db.GetItemAuthor(r.Context(), h.database, siteID, name, id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return sess, set, 0, false
	}
	if err != nil || author.SubmittedBy != sess.UserID || (live && author.DeletedAt != nil) {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "no entry of yours with that id", Code: "not_found"})
		return sess, set, 0, false
	}
	return sess, set, id, true
}

// itemKind reads the name of an item route: a Personal name has no items
// anyone addresses by id (answered: the refusal is written); everything else
// comes back to its handler.
func (h *SiteHandler) itemKind(w http.ResponseWriter, r *http.Request, siteID, siteName string) (db.DataSettings, bool) {
	name := strings.TrimSpace(r.PathValue("coll"))
	set, ok := h.dataSettings(w, r, siteID, name)
	if !ok {
		return set, true
	}
	if set.Kind == db.KindPersonal {
		writeWrongKind(w, name, set.Kind, "each visitor changes their own record with PUT, PATCH or DELETE /v1/sites/"+siteName+"/data/"+name)
		return set, true
	}
	return set, false
}

// itemSite resolves {sitename} for the item routes. Writes the 404.
func (h *SiteHandler) itemSite(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	w.Header().Set("Cache-Control", "private, no-store")
	siteName := strings.TrimSpace(r.PathValue("sitename"))
	name := strings.TrimSpace(r.PathValue("coll"))
	if siteName == "" || !validCollectionName.MatchString(name) {
		writePrivateNotFound(w)
		return "", "", false
	}
	siteID, err := h.resolveSiteID(r, siteName)
	if err != nil {
		writePrivateNotFound(w)
		return "", "", false
	}
	return siteID, siteName, true
}

// updateEntry is PATCH /v1/sites/{s}/data/{name}/items/{id}. The owner (key,
// connector, or signed in on the site) goes to the list's owner edit; a
// signed-in visitor changes their own entry: the fields sent are merged in (a
// null removes one); _submitted_by and _submitted_at never change.
func (h *SiteHandler) updateEntry(w http.ResponseWriter, r *http.Request) {
	siteID, siteName, ok := h.itemSite(w, r)
	if !ok {
		return
	}
	if set, answered := h.itemKind(w, r, siteID, siteName); answered {
		return
	} else if set.Kind == db.KindBoard {
		h.updateBoardItem(w, r, siteID, set)
		return
	}
	if h.isOwnerRequest(r, siteID) {
		h.updatePrivateItem(w, r)
		return
	}
	if h.refuseSuspendedSiteID(w, r, siteID) || h.refuseOffline(w, r, siteID) {
		return
	}
	sess, set, id, ok := h.ownEntry(w, r, siteID, siteName, true)
	if !ok {
		return
	}
	if !h.personSaverOK(w, r, siteID, sess.UserID) {
		return
	}
	maxBytes := h.entryMaxBytes(set)
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	var patch map[string]json.RawMessage
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&patch); err != nil || patch == nil || dec.More() {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: "item too large", Code: "item_too_large"})
			return
		}
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: `send a JSON object of the fields to change, e.g. {"guests": 3}`})
		return
	}
	errTooLarge := errors.New("too large")
	errNotObject := errors.New("not an object")
	actor := h.withAuthorEmail(r.Context(), db.Actor{ID: sess.UserID, Kind: actorVisitor})
	item, err := db.UpdateCollectionItemByID(r.Context(), h.database, siteID, set.Name, id, actor, h.siteMaxBytes(), func(old json.RawMessage) (json.RawMessage, error) {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(old, &fields); err != nil || fields == nil {
			return nil, errNotObject
		}
		for k, v := range patch {
			if slices.Contains(privateStampKeys, k) {
				continue
			}
			if string(bytes.TrimSpace(v)) == "null" {
				delete(fields, k)
				continue
			}
			fields[k] = v
		}
		next, err := json.Marshal(fields)
		if err != nil {
			return nil, err
		}
		if int64(len(next)) > maxBytes && len(next) > len(old) {
			return nil, errTooLarge
		}
		return next, nil
	})
	h.boundHistory(r, siteID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "no entry of yours with that id", Code: "not_found"})
	case errors.Is(err, errTooLarge):
		writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: "item too large", Code: "item_too_large"})
	case errors.Is(err, db.ErrSiteFull):
		h.writeSiteFull(w)
	case errors.Is(err, errNotObject):
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
	siteID, siteName, ok := h.itemSite(w, r)
	if !ok {
		return
	}
	kset, answered := h.itemKind(w, r, siteID, siteName)
	if answered {
		return
	}
	if h.isOwnerRequest(r, siteID) {
		h.deletePrivateItem(w, r)
		return
	}
	if kset.Kind == db.KindBoard {
		h.deleteBoardItem(w, r, siteID, kset)
		return
	}
	if h.refuseSuspendedSiteID(w, r, siteID) {
		return
	}
	sess, set, id, ok := h.ownEntry(w, r, siteID, siteName, true)
	if !ok {
		return
	}
	actor := h.withAuthorEmail(r.Context(), db.Actor{ID: sess.UserID, Kind: actorVisitor})
	found, err := db.SoftDeleteItem(r.Context(), h.database, siteID, set.Name, id, actor)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if !found {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "no entry of yours with that id", Code: "not_found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"withdrawn": id, "undo_minutes": h.savedData.WithdrawUndoMinutes})
}

// undoWithdraw is POST /v1/sites/{s}/data/{name}/items/{id}/undo: a signed-in
// visitor brings back an entry they withdrew themselves, within
// SAVED_DATA_WITHDRAW_UNDO_MINUTES. (The owner restores anything from
// Recently deleted instead.)
func (h *SiteHandler) undoWithdraw(w http.ResponseWriter, r *http.Request) {
	siteID, siteName, ok := h.itemSite(w, r)
	if !ok {
		return
	}
	if set, answered := h.itemKind(w, r, siteID, siteName); answered {
		return
	} else if set.Kind == db.KindBoard {
		h.undoBoardDelete(w, r, siteID, set)
		return
	}
	if h.refuseSuspendedSiteID(w, r, siteID) || h.refuseOffline(w, r, siteID) {
		return
	}
	sess, set, id, ok := h.ownEntry(w, r, siteID, siteName, false)
	if !ok {
		return
	}
	if !h.personSaverOK(w, r, siteID, sess.UserID) {
		return
	}
	actor := h.withAuthorEmail(r.Context(), db.Actor{ID: sess.UserID, Kind: actorVisitor})
	window := time.Duration(h.savedData.WithdrawUndoMinutes) * time.Minute
	declared := set.Kind == db.KindEntries
	item, done, err := db.UndoOwnWithdrawal(r.Context(), h.database, siteID, set.Name, id, sess.UserID, window, declared, declared && set.OnePerPerson, h.savedData.EntriesMax, actor, h.siteMaxBytes(), false)
	switch {
	case errors.Is(err, db.ErrOnePerPerson):
		writeJSON(w, http.StatusConflict, errorResponse{Error: "you have another entry here now (one per person); withdraw it first", Code: "one_per_person"})
	case errors.Is(err, db.ErrNameFull):
		writeJSON(w, http.StatusConflict, errorResponse{
			Error: fmt.Sprintf("this list is full (%d entries); the owner can delete entries or clear it to make room", h.savedData.EntriesMax),
			Code:  "list_full",
		})
	case errors.Is(err, db.ErrSiteFull):
		h.writeSiteFull(w)
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
	case !done:
		writeJSON(w, http.StatusConflict, errorResponse{
			Error: fmt.Sprintf("only an entry you withdrew yourself in the last %s can be brought back here; the site owner can restore older ones", config.Span(window)),
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
	publicOK := func(what string) bool {
		return h.confirmPublicOK(w, r, siteID, name, what, req.ConfirmPublic)
	}
	req.Kind = normalizeKind(req.Kind)
	prev, ok := h.dataSettings(w, r, siteID, name)
	if !ok {
		return
	}
	// Personal records belong to the people who saved them: a Personal name
	// becomes another kind (which the owner or everyone reads) only when it
	// holds none, not even in Recently deleted.
	if prev.Kind == db.KindPersonal && req.Kind != db.KindPersonal && validKind(req.Kind) {
		has, err := db.NameHasRows(r.Context(), h.database, siteID, name)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
			return
		}
		if has {
			writeJSON(w, http.StatusConflict, errorResponse{
				Error: fmt.Sprintf("%q is Personal and holds records that belong to the people who saved them, so it never becomes another kind while it holds any. Use another name, or empty it and delete what is in its Recently deleted for good first", name),
				Code:  "has_records",
			})
			return
		}
	}
	switch req.Kind {
	case db.KindContent:
		// A private list never becomes the page's public document: what
		// visitors sent there stays theirs and the owner's. It has to be
		// empty first (no confirm_public here: nothing private is exposed).
		if prev.Private {
			n, err := db.CountLiveItems(r.Context(), h.database, siteID, name)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
				return
			}
			if n > 0 {
				noun := "entries"
				if n == 1 {
					noun = "entry"
				}
				writeJSON(w, http.StatusConflict, map[string]any{
					"error": fmt.Sprintf("%q is private and holds %d %s that only you can read; page info is public, so a private name becomes page info only when it is empty. Use another name, or delete its entries first", name, n, noun),
					"code":  "has_entries", "count": n,
				})
				return
			}
		}
		if req.Visibility == "owner" || (req.OnePerPerson != nil && *req.OnePerPerson) || (req.Notify != "" && req.Notify != db.NotifyOff) {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "page info is always public and written only by the owner; visibility, one_per_person and notify apply to Submissions (kind entries)", Code: "invalid_kind"})
			return
		}
		if prev.Kind != db.KindContent {
			n, err := db.CountKindNames(r.Context(), h.database, siteID, db.KindContent, name)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
				return
			}
			if n >= h.savedData.ContentNamesMax {
				writeJSON(w, http.StatusConflict, errorResponse{Error: fmt.Sprintf("a site has at most %d page info names; keep related settings together in one document", h.savedData.ContentNamesMax), Code: "too_many_names"})
				return
			}
			many, err := db.NameHoldsItems(r.Context(), h.database, siteID, name, 1)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
				return
			}
			if many {
				writeJSON(w, http.StatusConflict, errorResponse{Error: fmt.Sprintf("%q holds several entries, and page info is one document; use another name, or clear the list first", name), Code: "has_entries"})
				return
			}
		}
		if err := db.DeclareData(r.Context(), h.database, siteID, name, db.KindContent, false, false, db.NotifyOff); err != nil {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"site": siteName, "name": name, "kind": db.KindContent, "label": kindLabel(db.KindContent),
			"message": "Page info: anyone who can open the site reads it; only you change it (PUT /v1/sites/" + siteName + "/data/" + name + ", update_data).",
		})
	case db.KindEntries:
		// A first declaration starts from the defaults: private to the owner,
		// a daily email while private (off when public), any number per
		// person. A re-declaration keeps what it does not name.
		private, notify, one := true, "", false
		if prev.Kind == db.KindEntries {
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
		case db.NotifyOff, db.NotifyEach, db.NotifyDaily:
			notify = req.Notify
		default:
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: `notify is "off", "each" (batched, soon after they arrive) or "daily" (a digest)`, Code: "invalid_kind"})
			return
		}
		if notify == "" {
			notify = db.NotifyDaily
			if !private {
				notify = db.NotifyOff
			}
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
				Error: strings.Replace(privateListNeedsDomain, "%s", h.siteDomain, 1),
				Code:  "custom_domain_required",
			})
			return
		}
		if !hasHome {
			writeJSON(w, http.StatusConflict, errorResponse{
				Error: "Submissions come from visitors who sign in, and visitors sign in on the site's own address: connect one first (a free <name>." + h.siteDomain + " address works, or your own domain)",
				Code:  "custom_domain_required",
			})
			return
		}
		if !private && !h.publicEntriesOK(w) {
			return
		}
		if prev.Private && !private && !publicOK(`public Submissions`) {
			return
		}
		if prev.Kind != db.KindEntries && !h.entriesNameRoom(w, r, siteID, name) {
			return
		}
		if err := db.DeclareData(r.Context(), h.database, siteID, name, db.KindEntries, private, one, notify); err != nil {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
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
			"site": siteName, "name": name, "kind": db.KindEntries, "label": kindLabel(db.KindEntries),
			"visibility": visibilityWord(private), "one_per_person": one, "notify": notify, "message": msg,
		})
	case db.KindPersonal, db.KindBoard:
		h.declarePersonalOrBoard(w, r, siteID, siteName, name, req.Kind, prev, req.Visibility, req.OnePerPerson, req.Notify, req.ConfirmPublic)
	default:
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: `kind is "entries" (Submissions: things visitors send), "content" (Page info: only the owner writes it), "mine" (Personal: one private record per signed-in visitor) or "board" (Shared board: a list signed-in visitors edit together)`, Code: "invalid_kind"})
	}
}

func validKind(k string) bool {
	switch k {
	case db.KindContent, db.KindEntries, db.KindPersonal, db.KindBoard:
		return true
	}
	return false
}

// declarePersonalOrBoard declares name Personal (mine) or Shared board
// (board). Neither takes options. Both need the site on an address of its
// own, where visitors sign in. A name that already holds data never becomes
// Personal (it would vanish from the owner's view); private entries become a
// (public) board only with confirm_public.
func (h *SiteHandler) declarePersonalOrBoard(w http.ResponseWriter, r *http.Request, siteID, siteName, name, kind string, prev db.DataSettings, visibility string, one *bool, notify string, confirmPublic bool) {
	if (visibility != "" && !(kind == db.KindBoard && visibility == "public")) || (one != nil && *one) || (notify != "" && notify != db.NotifyOff) {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "Personal and Shared board take no options: visibility, one_per_person and notify apply to Submissions (kind entries)", Code: "invalid_kind"})
		return
	}
	internal := func() { writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"}) }
	if prev.Kind != kind {
		if kind == db.KindPersonal {
			has, err := db.NameHasRows(r.Context(), h.database, siteID, name)
			if err != nil {
				internal()
				return
			}
			if has {
				writeJSON(w, http.StatusConflict, errorResponse{
					Error: fmt.Sprintf("%q already holds data (or has items in Recently deleted), and Personal records are private to each person, so it would disappear from your view. Use another name, or empty it and delete what is in its Recently deleted for good first", name),
					Code:  "has_entries",
				})
				return
			}
		}
		max, label := h.savedData.PersonalNamesMax, "Personal"
		if kind == db.KindBoard {
			max, label = h.savedData.BoardNamesMax, "Shared board"
		}
		n, err := db.CountKindNames(r.Context(), h.database, siteID, kind, name)
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
			Error: "visitors save here while signed in, and visitors sign in on the site's own address: connect one first (a free <name>." + h.siteDomain + " address works, or your own domain)",
			Code:  "custom_domain_required",
		})
		return
	}
	if kind == db.KindBoard && prev.Private && !h.confirmPublicOK(w, r, siteID, name, "a Shared board", confirmPublic) {
		return
	}
	if err := db.DeclareData(r.Context(), h.database, siteID, name, kind, false, false, db.NotifyOff); err != nil {
		internal()
		return
	}
	msg := fmt.Sprintf("Personal: each visitor signed in on https://%s keeps one private record here (at most %d KB), on any device; only they read or change it. You see how many people have one, never what they saved.", home.Host, h.savedData.PersonalMaxKB)
	if kind == db.KindBoard {
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

// confirmPublicOK: a private name that holds entries is made readable by
// anyone only when the owner says so (confirmed), knowing what that shows.
// Writes the 409 confirm_public (with the count) otherwise.
func (h *SiteHandler) confirmPublicOK(w http.ResponseWriter, r *http.Request, siteID, name, what string, confirmed bool) bool {
	if confirmed {
		return true
	}
	n, err := db.CountLiveItems(r.Context(), h.database, siteID, name)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return false
	}
	if n == 0 {
		return true
	}
	noun := "entries"
	if n == 1 {
		noun = "entry"
	}
	writeJSON(w, http.StatusConflict, map[string]any{
		"error": fmt.Sprintf("%q holds %d private %s that only you can read now. As %s, anyone who can open the site reads them: every field of each entry (who sent each stays visible only to you). "+
			"To go ahead, send the same request with \"confirm_public\": true; or use another name", name, n, noun, what),
		"code": "confirm_public", "count": n,
	})
	return false
}

// entriesNameRoom: the site may declare one more Submissions name
// (SAVED_DATA_ENTRIES_NAMES_MAX). Writes the refusal.
func (h *SiteHandler) entriesNameRoom(w http.ResponseWriter, r *http.Request, siteID, name string) bool {
	n, err := db.CountKindNames(r.Context(), h.database, siteID, db.KindEntries, name)
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
		return db.KindContent
	case "entries", "submissions":
		return db.KindEntries
	case "mine", "personal":
		return db.KindPersonal
	case "board", "shared board":
		return db.KindBoard
	}
	return strings.TrimSpace(k)
}

func notifyWords(notify string) string {
	switch notify {
	case db.NotifyEach:
		return "You get an email soon after new entries arrive (batched)."
	case db.NotifyDaily:
		return "You get a daily email when there are new entries."
	}
	return "No email about new entries."
}

// dataSummary is one name in the owner's list.
type dataSummary struct {
	db.CollectionSummary
	Label string `json:"label"`
}

// listData is GET /v1/sites/{s}/data: every data name with its kind and
// settings, who may save, and whether undeclared names take saves.
func (h *SiteHandler) listData(w http.ResponseWriter, r *http.Request) {
	_, siteID, siteName, _, ok := h.ownedDataSite(w, r)
	if !ok {
		return
	}
	summaries, err := db.ListCollectionSummariesByID(r.Context(), h.database, siteID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	savers, err := db.GetSavers(r.Context(), h.database, siteID)
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
	s, err := db.GetSavers(r.Context(), h.database, siteID)
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
	case "", db.SaversAnyone:
		req.Mode = db.SaversAnyone
	case db.SaversListed:
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
	s := db.Savers{Mode: req.Mode, Allow: allow, Block: req.Block}
	if err := db.SetSavers(r.Context(), h.database, siteID, s); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"site": siteName, "mode": s.Mode, "allow": s.Allow, "block": s.Block, "message": saversWords(s)})
}

func saversWords(s db.Savers) string {
	msg := "Anyone who signs in can save on this site."
	if s.Mode == db.SaversListed {
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
		author, err := db.GetItemAuthor(r.Context(), h.database, siteID, req.Collection, id)
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "no such entry", Code: "not_found"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
			return
		}
		if author.Email == "" || author.Email == db.OperatorLabel {
			writeJSON(w, http.StatusConflict, errorResponse{Error: "that entry was saved without a sign-in, so there is nobody to block", Code: "no_author"})
			return
		}
		// The address the server stamped, without a +tag, so the same
		// mailbox under another tag is blocked too.
		pattern = db.BaseEmail(author.Email)
	}
	norm, valid := normalizeSaver(pattern)
	if !valid {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "send an email address, a domain (@company.com), or an entry (collection and id)", Code: "invalid_savers"})
		return
	}
	cur, err := db.GetSavers(r.Context(), h.database, siteID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if !slices.Contains(cur.Block, norm) && len(cur.Allow)+len(cur.Block) >= h.savedData.SaversMax {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: fmt.Sprintf("at most %d emails and domains in total", h.savedData.SaversMax), Code: "too_many_savers"})
		return
	}
	n, err := db.AddBlocked(r.Context(), h.database, siteID, norm)
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
func (h *SiteHandler) StartSubmissionEmails(ctx context.Context) {
	go func() {
		t := time.NewTicker(time.Duration(h.savedData.NotifyEachMinutes) * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				h.sendSubmissionEmails(ctx)
			}
		}
	}()
}

func (h *SiteHandler) sendSubmissionEmails(ctx context.Context) int {
	mailer, ok := h.mailer.(noticeSender)
	if !ok {
		return 0
	}
	each := time.Duration(h.savedData.NotifyEachMinutes) * time.Minute
	daily := time.Duration(h.savedData.NotifyDailyHours) * time.Hour
	due, err := db.DueNotifications(ctx, h.database, each, daily)
	if err != nil {
		log.Printf("submission emails: %v", err)
		return 0
	}
	sent := 0
	for _, d := range due {
		to, err := db.GetSiteOwnerEmail(ctx, h.database, d.SiteID)
		if err != nil || !strings.Contains(to, "@") {
			continue
		}
		// Claimed before it is sent: another server (or the next tick after
		// a failed send) never sends the same email again.
		won, err := db.ClaimNotification(ctx, h.database, d)
		if err != nil {
			log.Printf("submission emails: claim site_id=%s name=%s: %v", d.SiteID, d.Name, err)
			continue
		}
		if !won {
			continue
		}
		noun := "entries"
		if d.Count == 1 {
			noun = "entry"
		}
		subject := fmt.Sprintf("%d new %s in %s on %s", d.Count, noun, d.Name, d.SiteName)
		owner := h.exportLinkBase() + "/"
		if d.Handle != "" {
			owner = h.mainSiteURL() + "/" + d.Handle
		}
		stop := h.exportLinkBase() + "/v1/data-notify/stop?t=" + url.QueryEscape(h.notifyStopToken(d.SiteID, d.Name))
		text := fmt.Sprintf(`%d new %s arrived in %s on your site %s since %s.

See who sent them and what they say in your sites page: %s

Stop these emails for %s: %s

Simple Host
`, d.Count, noun, d.Name, d.SiteName, d.Since.UTC().Format("2 Jan 2006 15:04 UTC"), owner, d.Name, stop)
		if err := mailer.SendNotice(to, subject, text); err != nil {
			// Not retried: the next email counts from here, so these entries
			// are left out of the count rather than mailed twice.
			log.Printf("submission emails: site_id=%s name=%s: %v", d.SiteID, d.Name, err)
			continue
		}
		sent++
	}
	if sent > 0 {
		log.Printf("submission emails: sent %d", sent)
	}
	return sent
}

// notifyStop is GET/POST /v1/data-notify/stop?t=: the emailed "stop these"
// link. GET shows a button (mail scanners follow links); POST turns the
// name's email off.
func (h *SiteHandler) notifyStop(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	tok := idleLinkToken(w, r)
	siteID, name, ok := h.checkNotifyStopToken(tok)
	if !ok {
		h.renderMessagePage(w, r, http.StatusNotFound, "This link does not work",
			"Sign in to Simple Host and change the email setting from your sites page.", h.exportLinkBase()+"/", "Go to Simple Host")
		return
	}
	if r.Method != http.MethodPost {
		writeMessagePage(w, r, h.chromeBase(r), http.StatusOK, "Stop emails about "+html.EscapeString(name)+"?",
			"You will not be emailed about new entries in "+html.EscapeString(name)+" any more. You can turn it back on from your sites page.",
			"", "", confirmForm("/v1/data-notify/stop", map[string]string{"t": tok}, "Stop these emails"))
		return
	}
	if _, err := db.SetNotify(r.Context(), h.database, siteID, name, db.NotifyOff); err != nil {
		h.renderServiceError(w)
		return
	}
	h.renderMessagePage(w, r, http.StatusOK, "Stopped: no more emails about "+html.EscapeString(name),
		"Turn them back on any time from your sites page.", h.exportLinkBase()+"/", "Go to Simple Host")
}
