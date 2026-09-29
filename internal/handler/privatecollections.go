package handler

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/auth"
	db "github.com/vsriram/simple-host/internal/db"
)

// Private collections (owner decision 2026-09-24; reverses "nothing gates
// reading" for these lists only).
//
// The owner marks a collection private. From then on:
//   - Submitting needs a visitor signed in on the site's OWN domain (a proven
//     custom domain or a claimed <name>.<SITE_DOMAIN>), from a page on that
//     same origin. API keys cannot submit, and the shared host refuses it.
//     The server stamps every item with the submitter's verified email
//     (_submitted_by) and the time (_submitted_at); whatever the page sent
//     under those keys is overwritten, and the account id is kept in a column
//     the request cannot reach (collection_items.submitted_by).
//   - Reading is the owner's: their API key or connector token (REST, MCP,
//     CSV export, the dashboard), or the owner signed in as a visitor on the
//     site's own domain. Everyone else — anonymous, other signed-in visitors,
//     other sites, scripts — gets 404.
//
// Browser access rides on the host-only __Host- visitor cookie, and is only
// honoured for same-origin requests: every site on the shared host and every
// claimed <name>.<SITE_DOMAIN> is "same-site" with every other, so SameSite=Lax
// alone would let a hostile page there send the cookie along.

const privateListNeedsDomain = "private lists need the site on its own domain — connect one first (a free <name>.%s address works, or your own domain)"

// errPrivateNotFound is the one answer anyone without access gets.
func writePrivateNotFound(w http.ResponseWriter) {
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found", "code": "not_found"})
}

// requestOrigin is the scheme://host the browser says the request came from:
// Origin, else the Referer's origin. Empty when neither is usable.
func requestOrigin(r *http.Request) string {
	if o := r.Header.Get("Origin"); o != "" && o != "null" {
		return o
	}
	if ref := r.Header.Get("Referer"); ref != "" {
		if u, err := url.Parse(ref); err == nil && u.Scheme != "" && u.Host != "" {
			return u.Scheme + "://" + u.Host
		}
	}
	return ""
}

// sameOriginRequest: the request was made by a page on this very origin. A
// browser cannot forge Origin/Referer or Sec-Fetch-Site; a script outside a
// browser can, but then it has no visitor cookie to go with them.
func sameOriginRequest(r *http.Request) bool {
	if sfs := r.Header.Get("Sec-Fetch-Site"); sfs != "" && sfs != "same-origin" {
		return false
	}
	o := requestOrigin(r)
	if o == "" {
		return false
	}
	u, err := url.Parse(o)
	if err != nil || u.Host == "" {
		return false
	}
	want := "http"
	if requestIsHTTPS(r) {
		want = "https"
	}
	return strings.EqualFold(u.Scheme, want) && strings.EqualFold(u.Host, r.Host)
}

// strictVisitorCookie is the visitor session cookie without the fallback
// visitorCookieValue allows: over HTTPS only the __Host- cookie counts, since
// a sibling host under the same parent domain can plant a plain sh_vsess
// cookie (Domain=parent) but can never set a __Host- one.
func strictVisitorCookie(r *http.Request) string {
	name := visitorCookieHTTP
	if requestIsHTTPS(r) {
		name = visitorCookieHost
	}
	if c, err := r.Cookie(name); err == nil {
		return c.Value
	}
	return ""
}

// strictVisitorSession returns the visitor signed in to siteID on this host,
// for a same-origin request carrying the __Host- cookie. ok=false otherwise.
func (h *SiteHandler) strictVisitorSession(r *http.Request, siteID string) (db.VisitorSession, bool) {
	if !sameOriginRequest(r) {
		return db.VisitorSession{}, false
	}
	id, err := hex.DecodeString(strictVisitorCookie(r))
	if err != nil || len(id) != 32 {
		return db.VisitorSession{}, false
	}
	sess, err := db.GetVisitorSession(r.Context(), h.database, id)
	if err != nil || !h.sessionValidFor(r, sess, siteID) {
		return db.VisitorSession{}, false
	}
	return sess, true
}

// visitorEmail is the address an account presents to other people's sites
// (who may save, blocks, one per person, the _submitted_by stamp, GET /me):
// its current sign-in email, which it proved by code when it signed up or
// moved there. A linked Google address is only used for an account whose
// name is not an address (an organiser-made event account), because an
// account can move off an address it no longer owns while the identity
// linked under it stays.
func visitorEmail(ctx context.Context, database *sql.DB, userID string) (string, error) {
	u, err := db.GetUserByID(ctx, database, userID)
	if err != nil {
		return "", err
	}
	if strings.Contains(u.Username, "@") {
		return u.Username, nil
	}
	identity, err := db.GetLatestOAuthIdentity(ctx, database, userID)
	if err == nil && identity.Email.Valid && identity.Email.String != "" {
		return identity.Email.String, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	return u.Username, nil
}

// onOwnDomain: this request arrived on the site's own origin — its proven own
// domain, else its owner's person address (siteHomeFor). Returns that home.
func (h *SiteHandler) onOwnDomain(r *http.Request, siteID string) (siteHome, bool, error) {
	home, ok, err := h.siteHomeFor(r.Context(), siteID)
	if err != nil {
		return home, false, err
	}
	// Every family address of the site is its own origin too (its main
	// address is the most specific one; the others keep working).
	if h.isSiteFamilyHost(r.Context(), siteID, requestHostName(r)) {
		if !ok {
			_, ownerID, _, err := db.GetSiteOwner(r.Context(), h.database, siteID)
			if err != nil {
				return siteHome{}, false, err
			}
			home = siteHome{Host: requestHostName(r), OwnerID: ownerID, IsDomain: true}
		}
		return home, true, nil
	}
	if !ok {
		return home, false, nil
	}
	// The same address under the other base counts while the base moves.
	return home, h.sameUserHost(home.Host, requestHostName(r)), nil
}

// ownerBrowserView reports, writing nothing, whether r comes from the site
// owner signed in as a visitor on the site's own address (the check
// ownerBrowserRead makes). Used to keep owner-only fields in public reads.
func (h *SiteHandler) ownerBrowserView(r *http.Request, siteID string) bool {
	if strictVisitorCookie(r) == "" || strings.EqualFold(requestHostName(r), h.contentHost) {
		return false
	}
	sess, ok := h.strictVisitorSession(r, siteID)
	if !ok {
		return false
	}
	info, here, err := h.onOwnDomain(r, siteID)
	return err == nil && here && sess.UserID == info.OwnerID
}

// ownerBrowserRead decides a non-key read of a private collection: only the
// site owner, signed in as a visitor on the site's own domain, from a page on
// that domain. Anyone else gets the 404 (written here) and ok=false.
//
// A submitter reading back their own items is deliberately not offered. It
// was first held back because the Google sign-in hand-off was not bound to
// the starting browser (login CSRF: a victim signed in as the attacker would
// submit into the attacker's view). That hand-off is now bound (nonce cookie,
// 2026-09-26), but the rule stands: a page shows the submitter what they sent
// from the POST answer.
func (h *SiteHandler) ownerBrowserRead(w http.ResponseWriter, r *http.Request, siteID string) bool {
	if strings.EqualFold(requestHostName(r), h.contentHost) {
		writePrivateNotFound(w)
		return false
	}
	info, here, err := h.onOwnDomain(r, siteID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return false
	}
	if !here {
		writePrivateNotFound(w)
		return false
	}
	sess, ok := h.strictVisitorSession(r, siteID)
	if !ok || sess.UserID != info.OwnerID {
		writePrivateNotFound(w)
		return false
	}
	_ = db.TouchVisitorSession(r.Context(), h.database, sess.ID)
	return true
}

// appendPrivate is POST to a private collection. Returns after answering.
func (h *SiteHandler) appendPrivate(w http.ResponseWriter, r *http.Request, siteID, siteName string, set db.DataSettings) {
	coll := set.Name
	w.Header().Set("Cache-Control", "private, no-store")
	// Offline, or a page opened as a preview: no entries, as on public lists
	// (visitorWriteOK). Keys cannot add to a private list at all (below).
	if h.refuseSuspendedSiteID(w, r, siteID) || h.refuseOffline(w, r, siteID) {
		return
	}
	if previewReferer(r) {
		writePreviewReadOnly(w)
		return
	}
	onShared := strings.EqualFold(requestHostName(r), h.contentHost)
	info, here, err := h.onOwnDomain(r, siteID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if info.IsDomain && !here && (onShared || h.isSiteHostName(requestHostName(r)) || h.isPersonHost(r.Context(), requestHostName(r)) || h.isFamilyHostName(requestHostName(r))) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"error": "this site saves on its own domain", "code": "use_custom_domain", "domain": info.Host,
		})
		return
	}
	if r.Header.Get("X-API-Key") != "" {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "this list is private: only visitors signed in on the site's own domain can add to it; API keys cannot",
			"code":  "private_visitor_only",
		})
		return
	}
	if !here {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "this list is private: it takes submissions only on the site's own address, from a signed-in visitor",
			"code":  "private_needs_own_domain",
		})
		return
	}
	sess, ok := h.strictVisitorSession(r, siteID)
	if !ok {
		h.logAnonWrite(r, siteID, siteName, writeRouteCollectionPost, coll, "on", "private_rejected")
		writeVisitorAuthRequired(w)
		return
	}
	if r.Header.Get(visitorCSRFHeader) != visitorCSRFValue {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "missing CSRF header", "code": "csrf_required"})
		return
	}
	if sess.UserID != info.OwnerID && !h.allowAppend(w, r) {
		return
	}
	email, err := visitorEmail(r.Context(), h.database, sess.UserID)
	if err != nil || email == "" {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	// Who may save (kinds.go): the owner always may.
	if sess.UserID != info.OwnerID && !h.saverOK(w, r, siteID, email) {
		return
	}

	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, h.entryMaxBytes(set)))
	var fields map[string]json.RawMessage
	if err == nil {
		dec := json.NewDecoder(bytes.NewReader(raw))
		if err = dec.Decode(&fields); err == nil && (fields == nil || dec.More()) {
			err = errors.New("not one object")
		}
	}
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: "item too large", Code: "item_too_large"})
			return
		}
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "a private list takes one JSON object per item"})
		return
	}
	if !storableJSON(w, raw) {
		return
	}
	claim, handled := h.idemBegin(w, r, siteID, "POST collections/"+coll, db.Actor{ID: sess.UserID}, raw, h.replayItem(w, r, siteID, coll))
	if handled {
		return
	}
	defer h.idemEnd(r, claim)
	// Server-stamped: whatever the page sent under these keys is replaced.
	by, _ := json.Marshal(email)
	at, _ := json.Marshal(time.Now().UTC().Format(time.RFC3339))
	fields["_submitted_by"] = by
	fields["_submitted_at"] = at
	body, err := json.Marshal(fields)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid item"})
		return
	}
	if !h.siteHasRoom(w, r, siteID, int64(len(body))) {
		return
	}
	item, ok := h.saveEntry(w, r, siteID, set, body, db.Actor{ID: sess.UserID, Email: email, Kind: actorVisitor})
	if !ok {
		return
	}
	_ = db.TouchVisitorSession(r.Context(), h.database, sess.ID)
	h.watchItemSize(r.Context(), siteID, len(body))
	h.idemSave(r, claim, http.StatusCreated, "", item.ID)
	writeJSON(w, http.StatusCreated, item)
}

// setCollectionPrivacy is PUT /v1/sites/{sitename}/collections/{coll}/privacy
// {"private": true|false}: the owner (API key or connector token) chooses.
// Making a list private needs the site on its own proven domain.
func (h *SiteHandler) setCollectionPrivacy(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}
	siteName := strings.TrimSpace(r.PathValue("sitename"))
	coll := strings.TrimSpace(r.PathValue("coll"))
	if siteName == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "site name is required"})
		return
	}
	if !validCollectionName.MatchString(coll) {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid collection name"})
		return
	}
	var req struct {
		Private *bool `json:"private"`
		// ConfirmPublic: the owner saw that the entries already there
		// become readable by anyone.
		ConfirmPublic bool `json:"confirm_public"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || req.Private == nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: `send {"private": true} or {"private": false}`})
		return
	}
	siteID, err := h.ownedSiteID(r, user, siteName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if h.refuseSuspendedSiteID(w, r, siteID) {
		return
	}
	set, ok := h.dataSettings(w, r, siteID, coll)
	if !ok {
		return
	}
	switch set.Kind {
	case db.KindContent, db.KindBoard:
		writeWrongKind(w, coll, set.Kind, "page info and Shared boards are always public; only Submissions (kind entries) can be private")
		return
	case db.KindPersonal:
		writeWrongKind(w, coll, set.Kind, "Personal records are always private, each to its own person")
		return
	}
	home, hasHome, err := h.siteHomeFor(r.Context(), siteID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if *req.Private && !set.Private && !h.signInNeededOK(w, "Private lists") {
		return
	}
	if *req.Private && !hasHome {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": strings.Replace(privateListNeedsDomain, "%s", h.handoutBase(), 1),
			"code":  "custom_domain_required",
		})
		return
	}
	if !*req.Private && (set.Kind == db.KindEntries || (set.Kind == "" && !set.Shared)) && !h.publicEntriesOK(w) {
		return
	}
	// On a site that needs kinds, choosing privacy declares the name as
	// Submissions (with the default email for that visibility). Making a
	// private list public is checked under the name's lock: while it holds
	// entries (Recently deleted too) only with confirm_public.
	setPrivacy := func() error {
		return db.SetCollectionPrivateLocked(r.Context(), h.database, siteID, coll, *req.Private, req.ConfirmPublic)
	}
	if set.Kind == "" && !set.Shared {
		if !h.entriesNameRoom(w, r, siteID, coll) {
			return
		}
		notify := db.NotifyDaily
		if !*req.Private {
			notify = db.NotifyOff
		}
		setPrivacy = func() error {
			return db.DeclareDataLocked(r.Context(), h.database, siteID, coll, db.KindEntries, *req.Private, false, notify, req.ConfirmPublic)
		}
	}
	if err := setPrivacy(); err != nil {
		var pe *db.PrivateItemsError
		if errors.As(err, &pe) {
			writePrivateItems(w, coll, pe, "a public list")
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	resp := map[string]any{"site": siteName, "collection": coll, "private": *req.Private}
	if *req.Private {
		resp["domain"] = home.Host
		resp["message"] = "Private: visitors signed in on https://" + home.Host + " can add to it; only you can read it."
	} else {
		resp["message"] = "Public: anyone can read this list again, including everything already in it. Who sent each entry (their email) stays private: only you see it."
	}
	writeJSON(w, http.StatusOK, resp)
}

// privateStampKeys are set by the server on submission and can never be set,
// changed or removed by anyone afterwards, owner included.
var privateStampKeys = []string{"_submitted_by", "_submitted_at"}

// privateManager authorizes an edit or delete of one item in a private list:
// the site owner (API key, connector token, or their visitor session on the
// site's own domain with X-SH-CSRF) or the platform admin (admin key or an
// is_admin account's key). It returns the site id, or ok=false after writing
// the answer — 404 for everyone else, so nothing is revealed.
func (h *SiteHandler) privateManager(w http.ResponseWriter, r *http.Request, siteName string) (string, bool) {
	if strings.EqualFold(requestHostName(r), h.contentHost) {
		writePrivateNotFound(w)
		return "", false
	}
	if key := r.Header.Get("X-API-Key"); key != "" {
		u, ok, err := h.resolveWriterKey(r.Context(), key)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
			return "", false
		}
		if !ok {
			writePrivateNotFound(w)
			return "", false
		}
		if u.IsAdmin {
			// Moderation: the platform admin may act on any site. With a
			// {handle} route the site is exact; otherwise the name lookup.
			id, err := h.resolveSiteID(r, siteName)
			if err != nil {
				writePrivateNotFound(w)
				return "", false
			}
			log.Printf("admin_private_edit user_id=%s site_id=%s route=%s %s", u.ID, id, r.Method, r.URL.Path)
			return id, true
		}
		site, err := db.GetSiteByUser(r.Context(), h.database, u.ID, siteName)
		if err != nil {
			writePrivateNotFound(w)
			return "", false
		}
		if handle := strings.TrimSpace(r.PathValue("handle")); handle != "" && !strings.EqualFold(handle, u.Handle.String) {
			writePrivateNotFound(w)
			return "", false
		}
		return site.ID, true
	}
	siteID, err := h.resolveSiteID(r, siteName)
	if err != nil {
		writePrivateNotFound(w)
		return "", false
	}
	if !h.ownerBrowserRead(w, r, siteID) {
		return "", false
	}
	if r.Header.Get(visitorCSRFHeader) != visitorCSRFValue {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "missing CSRF header", "code": "csrf_required"})
		return "", false
	}
	return siteID, true
}

// privateItemTarget resolves and authorizes {sitename}/{coll}/{id} for an edit
// or delete. Only the owner (or the platform admin) gets here. Deleting works
// in any list, public included, so spam can be removed (decision 2026-09-27);
// editing stays private-only, because a public entry is what its visitor
// wrote. Visitors still only append.
func (h *SiteHandler) privateItemTarget(w http.ResponseWriter, r *http.Request) (siteID, coll string, id int64, ok bool) {
	w.Header().Set("Cache-Control", "private, no-store")
	siteName := strings.TrimSpace(r.PathValue("sitename"))
	coll = strings.TrimSpace(r.PathValue("coll"))
	if siteName == "" || !validCollectionName.MatchString(coll) {
		writePrivateNotFound(w)
		return "", "", 0, false
	}
	siteID, ok = h.privateManager(w, r, siteName)
	if !ok {
		return "", "", 0, false
	}
	// A taken-down site's lists are kept as they are until it is restored.
	if h.refuseSuspendedSiteID(w, r, siteID) {
		return "", "", 0, false
	}
	set, err := db.GetDataSettings(r.Context(), h.database, siteID, coll)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return "", "", 0, false
	}
	// Nobody changes or deletes one person's Personal record but that
	// person; the owner may empty the whole name (clear).
	if set.Kind == db.KindPersonal {
		writePersonalOnly(w, coll)
		return "", "", 0, false
	}
	// A Shared board is edited item by item, the owner included.
	if !set.Private && set.Kind != db.KindBoard && r.Method != http.MethodDelete {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "items in a public list cannot be edited, only deleted; only items in a private list can be edited",
			"code":  "append_only",
		})
		return "", "", 0, false
	}
	id, err = strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writePrivateNotFound(w)
		return "", "", 0, false
	}
	return siteID, coll, id, true
}

// updatePrivateItem is PATCH .../collections/{coll}/items/{id}: merge the
// fields sent into the item (a field sent as null is removed). The server
// stamps (_submitted_by, _submitted_at) and the item's time never change.
func (h *SiteHandler) updatePrivateItem(w http.ResponseWriter, r *http.Request) {
	siteID, coll, id, ok := h.privateItemTarget(w, r)
	if !ok {
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxCollectionItemSize))
	var patch map[string]json.RawMessage
	if err == nil {
		dec := json.NewDecoder(bytes.NewReader(raw))
		if err = dec.Decode(&patch); err == nil && (patch == nil || dec.More()) {
			err = errors.New("not one object")
		}
	}
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: "item too large", Code: "item_too_large"})
			return
		}
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: `send a JSON object of the fields to change, e.g. {"status": "done"}`})
		return
	}
	if !storableJSON(w, raw) {
		return
	}
	errTooLarge := errors.New("too large")
	errNotObject := errors.New("not an object")
	item, err := db.UpdateCollectionItemByID(r.Context(), h.database, siteID, coll, id, h.managerActor(r, siteID), h.siteMaxBytes(), func(old json.RawMessage) (json.RawMessage, error) {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(old, &fields); err != nil || fields == nil {
			return nil, errNotObject
		}
		for k, v := range patch {
			if slices.Contains(privateStampKeys, k) {
				continue // never settable, whatever was sent
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
		if len(next) > maxCollectionItemSize {
			return nil, errTooLarge
		}
		return next, nil
	})
	h.boundHistory(r, siteID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writePrivateNotFound(w)
	case errors.Is(err, errTooLarge):
		writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: "item too large", Code: "item_too_large"})
	case errors.Is(err, db.ErrSiteFull):
		h.writeSiteFull(w)
	case errors.Is(err, errNotObject):
		writeJSON(w, http.StatusConflict, errorResponse{Error: "this item is not a JSON object, so it has no fields to change; delete it instead", Code: "not_an_object"})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
	default:
		writeJSON(w, http.StatusOK, item)
	}
}

// deletePrivateItem is DELETE .../collections/{coll}/items/{id}: the item
// moves to the list's Recently deleted, restorable for SAVED_DATA_UNDO_DAYS.
func (h *SiteHandler) deletePrivateItem(w http.ResponseWriter, r *http.Request) {
	siteID, coll, id, ok := h.privateItemTarget(w, r)
	if !ok {
		return
	}
	found, err := db.SoftDeleteItem(r.Context(), h.database, siteID, coll, id, h.managerActor(r, siteID))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if !found {
		writePrivateNotFound(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// clearCollection is DELETE /v1/sites/{sitename}/collections/{coll}: the owner
// (or the platform admin) empties one list, public or private. Its items move
// to the list's Recently deleted for SAVED_DATA_UNDO_DAYS. The
// body must repeat the list's name ({"confirm": "<coll>"}) so a stray call
// cannot wipe a list. The list's private/public setting stays. Visitors can
// never do this.
func (h *SiteHandler) clearCollection(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	siteName := strings.TrimSpace(r.PathValue("sitename"))
	coll := strings.TrimSpace(r.PathValue("coll"))
	if siteName == "" || !validCollectionName.MatchString(coll) {
		writePrivateNotFound(w)
		return
	}
	siteID, ok := h.privateManager(w, r, siteName)
	if !ok {
		return
	}
	if h.refuseSuspendedSiteID(w, r, siteID) {
		return
	}
	var req struct {
		Confirm string `json:"confirm"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || req.Confirm != coll {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": `to empty this list, send {"confirm": "` + coll + `"}`,
			"code":  "confirm_required",
		})
		return
	}
	n, err := db.SoftClearCollection(r.Context(), h.database, siteID, coll, h.managerActor(r, siteID))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"site": siteName, "collection": coll, "deleted": n, "restorable_days": h.savedData.UndoDays})
}
