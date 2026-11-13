package handler

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"time"

	appstorage "github.com/vsriram/simple-host/internal/app/storage"
	"github.com/vsriram/simple-host/internal/model"
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
	storageWhoNobody   = model.WhoNobody
	storageWhoOwner    = model.WhoOwner
	storageWhoOwn      = model.WhoOwn
	storageWhoSignedIn = model.WhoSignedIn
	storageWhoAnyone   = model.WhoAnyone
	storageWhoChecked  = model.WhoChecked
)

type storageAccessMatrix model.AccessMatrix
type storagePreset struct {
	ID     string
	Matrix storageAccessMatrix
}

var storagePresets = func() []storagePreset {
	out := make([]storagePreset, 0, len(model.Presets))
	for _, p := range model.Presets {
		out = append(out, storagePreset{p.ID, storageAccessMatrix(p.Matrix)})
	}
	return out
}()

func storagePresetMatrix(id string) (storageAccessMatrix, bool) {
	m, ok := model.PresetMatrix(id)
	return storageAccessMatrix(m), ok
}
func storagePresetName(m storageAccessMatrix) string  { return model.PresetName(model.AccessMatrix(m)) }
func (m storageAccessMatrix) value(a string) string   { return model.AccessMatrix(m).Value(a) }
func (m storageAccessMatrix) check() (string, string) { return model.AccessMatrix(m).Check() }
func (m storageAccessMatrix) usesOwn() bool           { return model.AccessMatrix(m).UsesOwn() }
func (m storageAccessMatrix) recordsAuthor() bool     { return model.AccessMatrix(m).RecordsAuthor() }
func storageTranslateOld(r, w, mode string) (storageAccessMatrix, string, string) {
	m, c, h := model.TranslateOld(r, w, mode)
	return storageAccessMatrix(m), c, h
}
func storageOldFields(m storageAccessMatrix) (string, string, string, bool) {
	return model.OldFields(model.AccessMatrix(m))
}

type storageTableRule struct {
	Rules      json.RawMessage
	Matrix     storageAccessMatrix
	PresetBase string
}
type storageAccessRequest model.AccessRequest

func (q storageAccessRequest) matrix() (storageAccessMatrix, string, string, string) {
	m, b, c, h := model.AccessRequest(q).Matrix()
	return storageAccessMatrix(m), b, c, h
}

var storageTableNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

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
// them.
func storageResourceJSON(x storageResource) map[string]any {
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
		out["approve_first"] = x.ApproveFirst
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

func (x storageResource) rulesResource() model.Resource {
	r := model.Resource{Name: x.Name, Kind: x.Kind, Read: x.Read, Write: x.Write, SitePasscode: x.SitePasscode, WriteMode: x.WriteMode, PresetBase: x.PresetBase, ApproveFirst: x.ApproveFirst, Rules: x.Rules, ConvertedFrom: x.ConvertedFrom}
	if x.Access != nil {
		m := model.AccessMatrix(*x.Access)
		r.Access = &m
	}
	r.TableLookup = func(n string) (model.TableRule, bool) {
		t, ok := x.Tables[n]
		return model.TableRule{Matrix: model.AccessMatrix(t.Matrix), PresetBase: t.PresetBase, Rules: t.Rules}, ok
	}
	r.TableRange = func(visit func(string, model.TableRule)) {
		for n, t := range x.Tables {
			visit(n, model.TableRule{Matrix: model.AccessMatrix(t.Matrix), PresetBase: t.PresetBase, Rules: t.Rules})
		}
	}
	return r
}
func (x storageResource) matrix() storageAccessMatrix {
	return storageAccessMatrix(x.rulesResource().Matrix())
}
func (x storageResource) legacy() bool           { return x.rulesResource().Legacy() }
func (x storageResource) legacyVisitorSQL() bool { return x.rulesResource().LegacyVisitorSQL() }
func (x storageResource) tableMatrix(t string) storageAccessMatrix {
	return storageAccessMatrix(x.rulesResource().TableMatrix(t))
}
func (x storageResource) ownTables() func(string) bool { return x.rulesResource().OwnTables() }
func (x storageResource) hasChecked() bool             { return x.rulesResource().HasChecked() }
func (x storageResource) holds(t string) bool          { return x.rulesResource().Holds(t) }
func (x storageResource) fingerprint() string          { return x.rulesResource().Fingerprint() }

func fromRulesResource(r model.Resource) storageResource {
	x := storageResource{Name: r.Name, Kind: r.Kind, Read: r.Read, Write: r.Write, SitePasscode: r.SitePasscode, WriteMode: r.WriteMode, PresetBase: r.PresetBase, ApproveFirst: r.ApproveFirst, Rules: r.Rules, ConvertedFrom: r.ConvertedFrom}
	if r.Access != nil {
		m := storageAccessMatrix(*r.Access)
		x.Access = &m
	}
	if r.Tables != nil {
		x.Tables = make(map[string]storageTableRule, len(r.Tables))
		for n, t := range r.Tables {
			x.Tables[n] = storageTableRule{Matrix: storageAccessMatrix(t.Matrix), PresetBase: t.PresetBase, Rules: t.Rules}
		}
	}
	return x
}
func (h *SiteHandler) loadStorageResource(ctx context.Context, siteID, name string) (storageResource, error) {
	r, e := h.storageSvc().LoadResource(ctx, appstorage.ResourceReadInput{SiteID: siteID, Name: name})
	return fromRulesResource(r), e
}

// storageOwnerOnSite reports whether a visitor session is the site's owner
// signed in on the site's own address (decision D1, 2026-10-10). Then the
// page acts with owner rights for storage data only (never settings,
// deploys, domains, passcodes, viewers, keys, versions or deleting the site;
// those routes require the owner's key). Conditions: the session was found by
// strictVisitorSession (same-origin, the __Host- cookie, bound to this site
// and this host); the account is the site's owner by id, so the email it
// signed in with is the account's own; the host is one of the site's own
// hosts (its site host, a family address, or its own domain), never the
// person host, the apex or the shared content host.
//
// On Simple Hack the owner is people, not the holding account (owner
// decision 2026-10-10): on a team site, an approved member of that team
// while the team may still change its site; on an event's custom website,
// an organiser of the event while it is neither ended nor taken down
// (db.HackSiteOwnerOnSite). After the deadline a member is an ordinary
// visitor there.
//
// Never from a page framed by another origin (auth.js sends X-SH-Framed: 1
// there; it covers custom domains and families, whose pages nginx serves
// without ownerFrameGuard) or from a preview of an earlier version.
func (h *SiteHandler) storageOwnerOnSite(r *http.Request, c storageCall) bool {
	if c.resource.Access == nil {
		return false
	}
	return h.storageSiteOwnerOnSite(r, c)
}

// storageSiteOwnerOnSite is storageOwnerOnSite without the one-resource
// preset check, for a site-level storage endpoint that names no resource
// (the people pick list, SIM-358).
func (h *SiteHandler) storageSiteOwnerOnSite(r *http.Request, c storageCall) bool {
	if c.visitorAI || c.visitorID == "" {
		return false
	}
	if !h.ed.Storage().EventOwners && c.visitorID != c.ownerID {
		return false
	}
	if r.Header.Get("X-SH-Framed") != "" || h.previewRefererFor(r, c.siteID) {
		return false
	}
	if !h.storageOwnHost(r, c.siteID) {
		return false
	}
	if h.ed.Storage().EventOwners {
		ok, err := h.storeAccounts().HackSiteOwnerOnSite(r.Context(), c.siteID, c.visitorID)
		return err == nil && ok
	}
	return true
}

// ownerFrameGuard: a page the site's owner opens while signed in on this
// host is never shown inside another origin's frame. There the page acts
// with owner rights for saved data (storageOwnerOnSite), and every
// <site>.<handle>.simple-host.app host is one "site" to a browser, so the
// Lax session cookie goes along when a sibling site frames it (clickjacking).
// Other visitors' pages are unchanged.
//
// On Simple Hack the owner is the team's members or the event's organisers
// (storageOwnerOnSite), and sibling team sites share one registrable domain
// the same way.
func (h *SiteHandler) ownerFrameGuard(w http.ResponseWriter, r *http.Request, ownerID string) {
	// A copy cached without the cookie must not stand in for one with it.
	w.Header().Add("Vary", "Cookie")
	switch r.Header.Get("Sec-Fetch-Dest") {
	case "empty", "image", "script", "style", "font", "audio", "video", "track", "manifest", "worker", "sharedworker", "serviceworker":
		return // never framed (empty: fetch and XHR); no database lookup per subresource
	}
	id, err := hex.DecodeString(strictVisitorCookie(r))
	if err != nil || len(id) != 32 {
		return
	}
	sess, err := h.storeAccounts().GetVisitorSession(r.Context(), id, h.limits.KeyIdleExpiry)
	if err != nil || !strings.EqualFold(sess.Host, requestHostName(r)) || time.Now().After(sess.ExpiresAt) {
		return
	}
	if h.ed.Storage().EventOwners {
		paperRecorder(r.Context()).HackMember(r.Context(), sess.UserID)
		if ok, err := h.storeAccounts().HackSiteOwnerOnSite(r.Context(), sess.SiteID, sess.UserID); err != nil || !ok {
			return
		}
	} else if sess.UserID != ownerID {
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
	// On Simple Hack the event's host is the holding account's person
	// host, and it serves the event's custom website.
	if _, person := h.personHostOwner(r.Context(), host); person && !h.ed.Storage().EventOwners {
		return false
	}
	id, err := h.resolveSiteIDScoped(r, r.PathValue("sitename"))
	return err == nil && id == siteID
}

// storageAllow decides one action for this request's caller under value.
// filter is "" when every entry is in reach, or the visitor id entries must
// carry (own). It writes the refusal itself.
func (h *SiteHandler) storageAllow(w http.ResponseWriter, r *http.Request, c *storageCall, action, value string) (string, bool) {
	call := h.storageServiceCall(*c)
	call.Context = r.Context()
	filter, err := h.storageSvc().Allow(&call, action, value)
	allowed := err == nil
	var refusals []*appstorage.Error
	if err != nil {
		e := err.(*appstorage.Error)
		allowed, refusals = e.Allowed, e.Refusals
	}
	c.activeChecked, c.checkedPost = call.ActiveChecked, call.CheckedPost
	writeStorageRefusals(w, refusals)
	return filter, allowed
}

// storageVisitorActive refuses a suspended visitor account and keeps the
// session alive.
func (h *SiteHandler) storageVisitorActive(w http.ResponseWriter, r *http.Request, c *storageCall) bool {
	if c.activeChecked {
		return true
	}
	suspended, err := h.storeAccounts().UserSuspended(r.Context(), c.visitorSession.UserID)
	if err != nil {
		storageError(w, 500, "internal_error", "internal server error")
		return false
	}
	if suspended {
		writeAccountSuspended(w)
		return false
	}
	_ = h.storeAccounts().TouchVisitorSession(r.Context(), c.visitorSession.ID, h.limits.VisitorSessionIdle)
	c.activeChecked = true
	return true
}

// storageGate runs the site-wide gates before any matrix decision, in
// today's order: the site's passcode, its named viewers, and CSRF on every
// change.
func (h *SiteHandler) storageGate(w http.ResponseWriter, r *http.Request, c storageCall, write bool) bool {
	call := h.storageServiceCall(c)
	call.CSRFOK = r.Header.Get("X-SH-CSRF") == "1"
	if err := h.storageRequestSvc(r, c).Gate(call, write); err != nil {
		e := err.(*appstorage.Error)
		storageError(w, http.StatusForbidden, e.Code, e.Message)
		return false
	}
	return true
}

// storageAccess is the gate plus one action on the resource's own matrix
// (KV, files, and legacy SQL). It records the action's filter on c.
func (h *SiteHandler) storageAccess(w http.ResponseWriter, r *http.Request, c *storageCall, action string) bool {
	call := h.storageServiceCall(*c)
	call.Context = r.Context()
	call.CSRFOK = r.Header.Get("X-SH-CSRF") == "1"
	filter, err := h.storageRequestSvc(r, *c).Access(&call, action)
	c.activeChecked, c.checkedPost = call.ActiveChecked, call.CheckedPost
	c.currentMatrix, c.filter = storageAccessMatrix(call.CurrentMatrix), filter
	if err == nil {
		return true
	}
	e := err.(*appstorage.Error)
	if len(e.Refusals) == 0 {
		storageError(w, http.StatusForbidden, e.Code, e.Message)
		return false
	}
	writeStorageRefusals(w, e.Refusals)
	return e.Allowed
}

// storageMayRead reports, without answering, whether the caller passes value
// and the filter that applies (foreign-key checks, storageTaken).
func (c storageCall) storageMayRead(value string) (string, bool) {
	if c.owner {
		return "", true
	}
	switch value {
	case storageWhoAnyone, storageWhoChecked:
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
