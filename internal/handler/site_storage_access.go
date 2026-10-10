package handler

import (
	"context"
	"database/sql"
	"encoding/hex"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/db"
)

// Storage access presets (owner decision 2026-10-10; INTENT.md and
// docs/designs/storage-access-presets.md). Every KV namespace, file bucket
// and SQLite table has an access matrix: four actions (read, add, edit,
// delete), each given one "who" value. Visitors never send SQL on a resource
// with a matrix; the server builds every statement from the matrix.
//
// The owner's tools (their key, the connector) always have full control.
// The site's owner signed in on the site's own address through visitor
// sign-in (D1) acts with owner rights for storage data only: see
// storageOwnerOnSite.

const (
	storageWhoNobody   = "nobody"
	storageWhoOwner    = "owner"
	storageWhoOwn      = "own"
	storageWhoSignedIn = "signed-in"
	storageWhoAnyone   = "anyone"
)

// Narrowest first. Each value includes the ones narrower than it, except
// nobody (which no page request passes).
var storageWhoRank = map[string]int{storageWhoNobody: 0, storageWhoOwner: 1, storageWhoOwn: 2, storageWhoSignedIn: 3, storageWhoAnyone: 4}

type storageAccessMatrix struct {
	Read   string `json:"read"`
	Add    string `json:"add"`
	Edit   string `json:"edit"`
	Delete string `json:"delete"`
}

type storagePreset struct {
	ID     string
	Matrix storageAccessMatrix
}

// The seven presets, exactly as designed. private is the default.
var storagePresets = []storagePreset{
	{"public", storageAccessMatrix{"anyone", "owner", "owner", "owner"}},
	{"inbox", storageAccessMatrix{"owner", "anyone", "owner", "owner"}},
	{"wall", storageAccessMatrix{"anyone", "signed-in", "owner", "own"}},
	{"records", storageAccessMatrix{"own", "signed-in", "owner", "owner"}},
	{"personal", storageAccessMatrix{"own", "signed-in", "own", "own"}},
	{"board", storageAccessMatrix{"signed-in", "signed-in", "signed-in", "owner"}},
	{"private", storageAccessMatrix{"owner", "owner", "owner", "owner"}},
}

func storagePresetMatrix(id string) (storageAccessMatrix, bool) {
	for _, p := range storagePresets {
		if p.ID == id {
			return p.Matrix, true
		}
	}
	return storageAccessMatrix{}, false
}

// storagePresetName is the preset a matrix is, or "custom".
func storagePresetName(m storageAccessMatrix) string {
	for _, p := range storagePresets {
		if p.Matrix == m {
			return p.ID
		}
	}
	return "custom"
}

func (m storageAccessMatrix) value(action string) string {
	switch action {
	case "read":
		return m.Read
	case "add":
		return m.Add
	case "edit":
		return m.Edit
	case "delete":
		return m.Delete
	}
	return storageWhoNobody
}

// check applies rules R1 to R4. It returns "" or the refused rule's code and
// a plain hint.
func (m storageAccessMatrix) check() (string, string) {
	for _, v := range []string{m.Read, m.Add, m.Edit, m.Delete} {
		if _, ok := storageWhoRank[v]; !ok {
			return "invalid_value", "each action takes nobody, owner, own, signed-in, or anyone"
		}
	}
	if m.Edit == storageWhoAnyone || m.Delete == storageWhoAnyone {
		return "anonymous_change", "R1: edit and delete are never anyone; someone not signed in could overwrite or wipe everything. Use the inbox preset (anyone adds, only you change) or wall (signed-in people add and delete their own)"
	}
	if m.Add == storageWhoOwn {
		return "add_own", "R2: add is never own, because nothing is owned before it exists; use signed-in, and the server records who added each entry"
	}
	if m.Add == storageWhoAnyone && (m.Read == storageWhoOwn || m.Edit == storageWhoOwn || m.Delete == storageWhoOwn) {
		return "own_needs_sign_in", "R3: an entry added by someone not signed in has no author, so own could never apply to it; set add to signed-in"
	}
	if storageWhoRank[m.Edit] > storageWhoRank[m.Read] || storageWhoRank[m.Delete] > storageWhoRank[m.Read] {
		return "change_wider_than_read", "R4: edit and delete cannot be wider than read; nobody may change what they cannot see"
	}
	return "", ""
}

func (m storageAccessMatrix) usesOwn() bool {
	return m.Read == storageWhoOwn || m.Edit == storageWhoOwn || m.Delete == storageWhoOwn
}

// recordsAuthor: entries need the server-owned visitor_id, because own
// applies or because visitors add (so the owner sees who sent what).
func (m storageAccessMatrix) recordsAuthor() bool {
	return m.usesOwn() || m.Add == storageWhoSignedIn || m.Add == storageWhoAnyone
}

func storageNarrower(a, b string) string {
	if storageWhoRank[a] < storageWhoRank[b] {
		return a
	}
	return b
}

// storageTranslateOld turns the older read/write/write_mode fields into a
// matrix, as designed (Compatibility). write anyone with full is refused.
func storageTranslateOld(read, write, mode string) (storageAccessMatrix, string, string) {
	if read == "" {
		read = storageWhoOwner
	}
	if write == "" {
		write = storageWhoOwner
	}
	if mode == "" {
		mode = "full"
	}
	if read != "anyone" && read != "signed-in" && read != "owner" && read != "own" || !validStoragePolicy(write) || mode != "full" && mode != "add" {
		return storageAccessMatrix{}, "invalid_value", "read takes anyone, signed-in, own, or owner; write takes anyone, signed-in, or owner; write_mode takes full or add"
	}
	var m storageAccessMatrix
	switch {
	case mode == "add":
		m = storageAccessMatrix{read, write, storageWhoOwner, storageWhoOwner}
		if read == storageWhoOwn && write == storageWhoAnyone {
			m.Add = storageWhoSignedIn
		}
	case write == storageWhoAnyone:
		return m, "anonymous_change", "write anyone with write_mode full let anyone overwrite or delete everything and is no longer offered; use the inbox preset (anyone adds, only you change) or wall (signed-in people add and delete their own)"
	case write == storageWhoSignedIn:
		x := storageNarrower(storageWhoSignedIn, read)
		m = storageAccessMatrix{read, storageWhoSignedIn, x, x}
	default:
		m = storageAccessMatrix{read, storageWhoOwner, storageWhoOwner, storageWhoOwner}
	}
	if code, hint := m.check(); code != "" {
		return m, code, hint
	}
	return m, "", ""
}

// storageLegacyMatrix is how a resource saved before presets behaves until
// the owner saves a policy on it: exactly the old rules, unchecked (a
// write-anyone full resource stays open). Simple Hack resources always use it.
func storageLegacyMatrix(read, write, mode string) storageAccessMatrix {
	if mode == "add" {
		m := storageAccessMatrix{read, write, storageWhoOwner, storageWhoOwner}
		if read == storageWhoOwn && write == storageWhoAnyone {
			m.Add = storageWhoSignedIn
		}
		return m
	}
	if read == storageWhoOwn {
		return storageAccessMatrix{read, storageWhoOwner, storageWhoOwner, storageWhoOwner}
	}
	return storageAccessMatrix{read, write, write, write}
}

// storageOldFields expresses a matrix in the older fields when it can, so an
// older skill reading the answer is not confused.
func storageOldFields(m storageAccessMatrix) (read, write, mode string, ok bool) {
	read = m.Read
	if read == storageWhoNobody {
		read = storageWhoOwner
	}
	switch {
	case m.Add == m.Edit && m.Edit == m.Delete && validStoragePolicy(m.Add):
		return read, m.Add, "full", true
	case m.Edit == storageWhoOwner && m.Delete == storageWhoOwner && validStoragePolicy(m.Add):
		return read, m.Add, "add", true
	}
	return read, "", "", false
}

// storageTableRule is one SQLite table's own matrix.
type storageTableRule struct {
	Matrix     storageAccessMatrix
	PresetBase string
}

// storageAccessRequest is a preset plus single-action overrides.
type storageAccessRequest struct {
	Preset string `json:"preset"`
	Read   string `json:"read"`
	Add    string `json:"add"`
	Edit   string `json:"edit"`
	Delete string `json:"delete"`
}

func (q storageAccessRequest) matrix() (storageAccessMatrix, string, string, string) {
	base := q.Preset
	if base == "" {
		base = "private"
	}
	if base == "custom" {
		if q.Read == "" || q.Add == "" || q.Edit == "" || q.Delete == "" {
			return storageAccessMatrix{}, "", "invalid_preset", "a custom matrix sets all four of read, add, edit, and delete"
		}
		base = ""
	}
	m := storageAccessMatrix{}
	if base != "" {
		var ok bool
		if m, ok = storagePresetMatrix(base); !ok {
			return m, "", "invalid_preset", "preset takes public, inbox, wall, records, personal, board, or private"
		}
	}
	for _, o := range []struct {
		v   string
		dst *string
	}{{q.Read, &m.Read}, {q.Add, &m.Add}, {q.Edit, &m.Edit}, {q.Delete, &m.Delete}} {
		if o.v != "" {
			*o.dst = o.v
		}
	}
	if code, hint := m.check(); code != "" {
		return m, "", code, hint
	}
	if q.Preset == "" && q.Read == "" && q.Add == "" && q.Edit == "" && q.Delete == "" {
		base = "private"
	}
	return m, base, "", ""
}

var storageTableNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

const storageTablesMax = 50

// storageAccessJSON is a matrix's preset label and fields for a response.
func storageAccessJSON(m storageAccessMatrix, base string, legacy bool) map[string]any {
	out := map[string]any{"access": m}
	name := storagePresetName(m)
	switch {
	case legacy:
		out["preset"] = "legacy"
	case name == "custom":
		out["preset"] = "custom"
		if base != "" && base != "custom" {
			out["based_on"] = base
		}
	default:
		out["preset"] = name
	}
	return out
}

// storageResourceJSON is a resource as the owner's tools see it: the preset
// (or custom, or legacy), the matrix, per-table rules for SQLite, and the
// older read/write/write_mode fields whenever the matrix can be expressed in
// them. Simple Hack answers in the older fields only.
func storageResourceJSON(x storageResource) map[string]any {
	if hackMode {
		return map[string]any{"name": x.Name, "kind": x.Kind, "read": x.Read, "write": x.Write, "site_passcode": x.SitePasscode, "write_mode": x.WriteMode}
	}
	m := x.matrix()
	out := storageAccessJSON(m, x.PresetBase, x.legacy())
	out["name"], out["kind"], out["site_passcode"] = x.Name, x.Kind, x.SitePasscode
	if x.Access == nil {
		out["read"], out["write"], out["write_mode"] = x.Read, x.Write, x.WriteMode
	} else if read, write, mode, ok := storageOldFields(m); ok {
		out["read"], out["write"], out["write_mode"] = read, write, mode
	} else {
		out["read"] = m.Read
	}
	if x.Kind == "sqlite" {
		tables := map[string]any{}
		for name, rule := range x.Tables {
			tables[name] = storageAccessJSON(rule.Matrix, rule.PresetBase, false)
		}
		out["tables"] = tables
	}
	if x.legacy() {
		out["legacy_note"] = "saved before presets; it keeps its old rules until you set a preset on it"
	}
	return out
}

// matrix is the resource's own matrix (the SQLite database default).
func (x storageResource) matrix() storageAccessMatrix {
	if x.Access != nil {
		return *x.Access
	}
	return storageLegacyMatrix(x.Read, x.Write, x.WriteMode)
}

// legacy: saved before presets with rules the new model refuses (write
// anyone with full), or a full-mode SQLite database that pages still query
// with SQL. Either keeps working until the owner saves a policy on it.
func (x storageResource) legacy() bool {
	if x.Access != nil || hackMode {
		return false
	}
	return x.WriteMode == "full" && x.Write == storageWhoAnyone || x.legacyVisitorSQL()
}

// legacyVisitorSQL: pages may still send SQL to /query and /execute. Only a
// full-mode SQLite database saved before presets with visitor read or
// write; never on a resource with a matrix.
func (x storageResource) legacyVisitorSQL() bool {
	return x.Access == nil && x.Kind == "sqlite" && x.WriteMode == "full" && x.Read != storageWhoOwn && (x.Read != storageWhoOwner || x.Write != storageWhoOwner)
}

// tableMatrix is the matrix for one table: its own entry, or the database
// default.
func (x storageResource) tableMatrix(table string) storageAccessMatrix {
	if x.Access != nil {
		// SQLite names are case-insensitive; rules are stored lower-case.
		if rule, ok := x.Tables[strings.ToLower(table)]; ok {
			return rule.Matrix
		}
	}
	return x.matrix()
}

// ownTables picks the tables that need visitor_id: nil (every table) on a
// legacy read-own database, else those whose matrix uses own or lets
// visitors add.
func (x storageResource) ownTables() func(string) bool {
	if x.Access == nil {
		return nil
	}
	return func(table string) bool { return x.tableMatrix(table).recordsAuthor() }
}

// fingerprint compares two loads of the same resource (storageWriteLock).
func (x storageResource) fingerprint() string {
	parts := []string{x.Name, x.Kind, x.Read, x.Write, x.SitePasscode, x.WriteMode, x.PresetBase}
	if x.Access != nil {
		parts = append(parts, x.Access.Read, x.Access.Add, x.Access.Edit, x.Access.Delete)
	} else {
		parts = append(parts, "-")
	}
	names := make([]string, 0, len(x.Tables))
	for n := range x.Tables {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		r := x.Tables[n]
		parts = append(parts, n, r.Matrix.Read, r.Matrix.Add, r.Matrix.Edit, r.Matrix.Delete, r.PresetBase)
	}
	return strings.Join(parts, "\x00")
}

const storageResourceColumns = `name,kind,read_policy,write_policy,site_passcode,write_mode,acc_read,acc_add,acc_edit,acc_delete,preset_base`

func scanStorageResource(scan func(...any) error) (storageResource, error) {
	var x storageResource
	var r, a, e, d sql.NullString
	if err := scan(&x.Name, &x.Kind, &x.Read, &x.Write, &x.SitePasscode, &x.WriteMode, &r, &a, &e, &d, &x.PresetBase); err != nil {
		return x, err
	}
	if r.Valid && a.Valid && e.Valid && d.Valid {
		x.Access = &storageAccessMatrix{r.String, a.String, e.String, d.String}
	}
	return x, nil
}

// loadStorageResource reads one resource and, for SQLite, its table rules.
func (h *SiteHandler) loadStorageResource(ctx context.Context, siteID, name string) (storageResource, error) {
	x, err := scanStorageResource(h.database.QueryRowContext(ctx, `SELECT `+storageResourceColumns+` FROM site_storage_resources WHERE site_id=$1 AND name=$2`, siteID, name).Scan)
	if err != nil {
		return x, err
	}
	if x.Kind == "sqlite" && x.Access != nil {
		x.Tables, err = h.loadStorageTables(ctx, siteID, name)
	}
	return x, err
}

func (h *SiteHandler) loadStorageTables(ctx context.Context, siteID, name string) (map[string]storageTableRule, error) {
	rows, err := h.database.QueryContext(ctx, `SELECT table_name,acc_read,acc_add,acc_edit,acc_delete,preset_base FROM site_storage_tables WHERE site_id=$1 AND resource_name=$2`, siteID, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]storageTableRule{}
	for rows.Next() {
		var n string
		var t storageTableRule
		if err := rows.Scan(&n, &t.Matrix.Read, &t.Matrix.Add, &t.Matrix.Edit, &t.Matrix.Delete, &t.PresetBase); err != nil {
			return nil, err
		}
		out[n] = t
	}
	return out, rows.Err()
}

// storageOwnerOnSite reports whether a visitor session is the site's owner
// signed in on the site's own address (decision D1, 2026-10-10). Then the
// page acts with owner rights for storage data only (never settings,
// deploys, domains, passcodes, viewers, keys, versions or deleting the site;
// those routes require the owner's key). Conditions: Simple Host only; the
// session was found by strictVisitorSession (same-origin, the __Host- cookie,
// bound to this site and this host); the account is the site's owner by id,
// so the email it signed in with is the account's own; the host is one of
// the site's own hosts (its site host, a family address, or its own domain),
// never the person host, the apex or the shared content host.
//
// Never from a page framed by another origin (auth.js sends X-SH-Framed: 1
// there; it covers custom domains and families, whose pages nginx serves
// without ownerFrameGuard) or from a preview of an earlier version.
func (h *SiteHandler) storageOwnerOnSite(r *http.Request, c storageCall) bool {
	if hackMode || c.visitorID == "" || c.visitorID != c.ownerID || c.resource.Access == nil {
		return false
	}
	if r.Header.Get("X-SH-Framed") != "" || h.previewRefererFor(r, c.siteID) {
		return false
	}
	return h.storageOwnHost(r, c.siteID)
}

// ownerFrameGuard: a page the site's owner opens while signed in on this
// host is never shown inside another origin's frame. There the page acts
// with owner rights for saved data (storageOwnerOnSite), and every
// <site>.<handle>.simple-host.app host is one "site" to a browser, so the
// Lax session cookie goes along when a sibling site frames it (clickjacking).
// Other visitors' pages are unchanged.
func (h *SiteHandler) ownerFrameGuard(w http.ResponseWriter, r *http.Request, ownerID string) {
	if hackMode {
		return
	}
	// A copy cached without the cookie must not stand in for one with it.
	w.Header().Add("Vary", "Cookie")
	switch r.Header.Get("Sec-Fetch-Dest") {
	case "image", "script", "style", "font", "audio", "video", "track", "manifest", "worker", "sharedworker", "serviceworker":
		return // never framed; no database lookup per subresource
	}
	id, err := hex.DecodeString(strictVisitorCookie(r))
	if err != nil || len(id) != 32 {
		return
	}
	sess, err := db.GetVisitorSession(r.Context(), h.database, id)
	if err != nil || sess.UserID != ownerID || !strings.EqualFold(sess.Host, requestHostName(r)) || time.Now().After(sess.ExpiresAt) {
		return
	}
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	if !strings.Contains(strings.Join(w.Header().Values("Content-Security-Policy"), ";"), "frame-ancestors 'self'") {
		w.Header().Add("Content-Security-Policy", "frame-ancestors 'self'")
	}
}

func (h *SiteHandler) storageOwnHost(r *http.Request, siteID string) bool {
	host := requestHostName(r)
	if host == "" || h.isVisitorApexHost(host) || strings.EqualFold(host, h.contentHost) {
		return false
	}
	if _, person := h.personHostOwner(r.Context(), host); person {
		return false
	}
	id, err := h.resolveSiteIDScoped(r, r.PathValue("sitename"))
	return err == nil && id == siteID
}

// storageAllow decides one action for this request's caller under value.
// filter is "" when every entry is in reach, or the visitor id entries must
// carry (own). It writes the refusal itself.
func (h *SiteHandler) storageAllow(w http.ResponseWriter, r *http.Request, c *storageCall, action, value string) (string, bool) {
	if c.owner {
		return "", true
	}
	switch value {
	case storageWhoAnyone:
		return "", true
	case storageWhoOwn, storageWhoSignedIn:
		if c.visitorID == "" {
			storageError(w, 401, "sign_in_required", "sign in to this site first")
			return "", false
		}
		if !h.storageVisitorActive(w, r, c) {
			return "", false
		}
		if c.ownerOnSite || value == storageWhoSignedIn {
			return "", true
		}
		return c.visitorID, true
	case storageWhoOwner:
		if c.ownerOnSite && h.storageVisitorActive(w, r, c) {
			return "", true
		}
		if c.ownerOnSite {
			return "", false
		}
	}
	if (action == "edit" || action == "delete") && value != storageWhoNobody {
		if add := c.currentMatrix.Add; add == storageWhoAnyone || add == storageWhoSignedIn && c.visitorID != "" {
			storageError(w, 403, "add_only", "visitors can add here, not change or delete")
			return "", false
		}
	}
	storageError(w, 403, "forbidden", action+" is not open to this visitor here ("+value+")")
	return "", false
}

// storageVisitorActive refuses a suspended visitor account and keeps the
// session alive.
func (h *SiteHandler) storageVisitorActive(w http.ResponseWriter, r *http.Request, c *storageCall) bool {
	if c.activeChecked {
		return true
	}
	suspended, err := db.UserSuspended(r.Context(), h.database, c.visitorSession.UserID)
	if err != nil {
		storageError(w, 500, "internal_error", "internal server error")
		return false
	}
	if suspended {
		writeAccountSuspended(w)
		return false
	}
	_ = db.TouchVisitorSession(r.Context(), h.database, c.visitorSession.ID)
	c.activeChecked = true
	return true
}

// storageGate runs the site-wide gates before any matrix decision, in
// today's order: the site's passcode, its named viewers, and CSRF on every
// change.
func (h *SiteHandler) storageGate(w http.ResponseWriter, r *http.Request, c storageCall, write bool) bool {
	if c.owner {
		return true
	}
	if c.resource.SitePasscode == "inherit" && !h.PasscodeLetsIn(r, c.siteID) {
		storageError(w, 403, "site_locked", "unlock this site first")
		return false
	}
	// A named-viewers site (viewers.go) keeps all of its saved data to the
	// owner and its named viewers, whatever the resource's site_passcode says.
	if !h.ViewersLetIn(r, c.siteID) {
		storageError(w, 403, "site_private", "this site is open only to its named viewers; sign in on the site first")
		return false
	}
	if write && r.Header.Get("X-SH-CSRF") != "1" {
		storageError(w, 403, "csrf_required", "X-SH-CSRF: 1 required")
		return false
	}
	return true
}

// storageAccess is the gate plus one action on the resource's own matrix
// (KV, files, and legacy SQL). It records the action's filter on c.
func (h *SiteHandler) storageAccess(w http.ResponseWriter, r *http.Request, c *storageCall, action string) bool {
	if !h.storageGate(w, r, *c, action != "read") {
		return false
	}
	c.currentMatrix = c.resource.matrix()
	filter, ok := h.storageAllow(w, r, c, action, c.currentMatrix.value(action))
	c.filter = filter
	return ok
}

// storageMayRead reports, without answering, whether the caller passes value
// and the filter that applies (foreign-key checks, storageTaken).
func (c storageCall) storageMayRead(value string) (string, bool) {
	if c.owner {
		return "", true
	}
	switch value {
	case storageWhoAnyone:
		return "", true
	case storageWhoSignedIn:
		return "", c.visitorID != ""
	case storageWhoOwn:
		if c.visitorID == "" {
			return "", false
		}
		if c.ownerOnSite {
			return "", true
		}
		return c.visitorID, true
	case storageWhoOwner:
		return "", c.ownerOnSite
	}
	return "", false
}

// storageTaken: the caller may add but not edit, so an existing key or path
// answers 409 (taken), as add-only did before presets.
func (c storageCall) storageTaken(m storageAccessMatrix) bool {
	if _, edit := c.storageMayRead(m.Edit); edit {
		return false
	}
	_, add := c.storageMayRead(m.Add)
	return add
}
