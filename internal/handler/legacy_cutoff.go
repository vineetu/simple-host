package handler

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/vsriram/simple-host/internal/app/legacy"
)

const legacyStorageClosedCode = "legacy_storage_closed"

type legacyStorageCallLog struct {
	mu   sync.Mutex
	day  string
	seen map[string]bool
}

// LegacyStorageCutoffGate refuses the old saved-data routes for sites created
// on or after LEGACY_STORAGE_CUTOFF. It wraps both public requests and the
// MCP's in-process calls. Empty/off cutoff, Hack mode (the product gate
// already answers), OPTIONS, and the instance-wide notify/watch paths pass
// through. Resolution errors and missing sites pass through so the real
// handler answers as today.
func (h *SiteHandler) LegacyStorageCutoffGate(next http.Handler) http.Handler {
	calls := &h.legacyStorageCalls
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.ed.Storage().LegacyRoutes {
			next.ServeHTTP(w, r)
			return
		}
		p := parseLegacyStoragePath(r.URL.Path)
		if h.legacySvc().CutoffApplies(h.ed.Storage().LegacyRoutes, r.Method == http.MethodOptions, p.match, p.skip) {
			if name, closed := h.legacyStorageClosedFor(r, p); closed {
				h.writeLegacyStorageClosed(w, name)
				return
			}
		}
		// Only operation resolvers may supply the logged ID. Cutoff candidates
		// are deliberately broader and are never evidence of a site's use.
		var siteID string
		if p.match && !p.skip && h.database != nil {
			r = r.WithContext(context.WithValue(r.Context(), legacyCallSiteKey{}, &siteID))
		}
		next.ServeHTTP(w, r)
		if siteID == "" {
			return
		}
		calls.mu.Lock()
		defer calls.mu.Unlock()
		today := time.Now().UTC().Format("2006-01-02")
		if calls.day != today {
			calls.day = today
			calls.seen = map[string]bool{}
		}
		if !calls.seen[siteID] {
			calls.seen[siteID] = true
			log.Printf("legacy_storage_call site=%s", siteID)
		}
	})
}

type legacyCallSiteKey struct{}

// recordLegacyCallSite receives a canonical ID from an operation's resolver,
// after authentication for owner/admin operations. Later resolution replaces
// earlier lookups so only the selected site becomes notice evidence.
func recordLegacyCallSite(r *http.Request, siteID string) {
	if selected, ok := r.Context().Value(legacyCallSiteKey{}).(*string); ok && siteID != "" {
		*selected = siteID
	}
}

func (h *SiteHandler) writeLegacyStorageClosed(w http.ResponseWriter, siteName string) {
	if siteName == "" {
		siteName = "{site}"
	}
	day := h.legacyStorageCutoff.UTC().Format("2006-01-02")
	storage := "/v1/sites/" + siteName + "/storage/resources"
	writeJSON(w, http.StatusGone, errorResponse{
		Error:   fmt.Sprintf("Saved state, collections and declared data are not available to sites created on or after %s. Use storage resources (KV, SQLite and files): PUT /v1/sites/%s/storage/resources/{name}, then the storage routes. See https://simple-host.app/llms.txt", day, siteName),
		Code:    legacyStorageClosedCode,
		Storage: storage,
	})
}

// legacyStorageClosedFor refuses only when every site the request could act
// on was created on or after the cutoff. When the resolvers disagree (a key
// that owns an older same-named site, a person host's home alias), one older
// candidate lets the request through: an existing site must never lose its
// saved data to this gate.
func (h *SiteHandler) legacyStorageClosedFor(r *http.Request, p legacyStoragePath) (string, bool) {
	return h.legacySvc().ClosedFor(r.Context(), h.legacyCutoffCandidates(r, p))
}

func (h *SiteHandler) legacyCutoffCandidates(r *http.Request, p legacyStoragePath) []legacy.CutoffCandidate {
	ctx := r.Context()
	var out []legacy.CutoffCandidate
	add := func(id, name string) {
		out = append(out, legacy.CutoffCandidate{ID: id, Name: name})
	}
	if p.adminID != "" {
		add(p.adminID, "")
		return out
	}
	rr := r
	if p.handle != "" || p.site != "" {
		rr = r.Clone(r.Context())
		if p.handle != "" {
			rr.SetPathValue("handle", p.handle)
		}
		if p.site != "" {
			rr.SetPathValue("sitename", p.site)
		}
	}
	if p.handle != "" {
		u, err := h.storeAccounts().GetUserByHandleOrAlias(ctx, p.handle)
		if err != nil {
			return out
		}
		if s, err := h.sitesSvc().GetSiteByUser(ctx, u.ID, p.site); err == nil {
			add(s.ID, s.Name)
			return out
		}
		// Not a literal site of that person: the handler may still resolve
		// it (a person host's home alias), so ask the same resolver.
		if id, _, err := h.resolveSiteIDBare(rr, p.site); err == nil {
			add(id, p.site)
		}
		return out
	}
	if id, _, err := h.resolveSiteIDBare(rr, p.site); err == nil {
		add(id, p.site)
	}
	if strings.TrimSpace(r.Header.Get("X-API-Key")) != "" {
		if id, ok := h.ownerSiteIDFromKey(rr, p.site); ok {
			add(id, p.site)
		}
	}
	return out
}
