package handler

import (
	"fmt"
	"net/http"
	"strings"

	db "github.com/vsriram/simple-host/internal/db"
)

const legacyStorageClosedCode = "legacy_storage_closed"

// LegacyStorageCutoffGate refuses the old saved-data routes for sites created
// on or after LEGACY_STORAGE_CUTOFF. It wraps both public requests and the
// MCP's in-process calls. Empty/off cutoff, Hack mode (HackLegacyStorageGate
// already answers), OPTIONS, and the instance-wide notify/watch paths pass
// through. Resolution errors and missing sites pass through so the real
// handler answers as today.
func (h *SiteHandler) LegacyStorageCutoffGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.legacyStorageCutoff.IsZero() || hackMode || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		p := parseLegacyStoragePath(r.URL.Path)
		if !p.match || p.skip {
			next.ServeHTTP(w, r)
			return
		}
		if name, closed := h.legacyStorageClosedFor(r, p); closed {
			h.writeLegacyStorageClosed(w, name)
			return
		}
		next.ServeHTTP(w, r)
	})
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
	cutoff := h.legacyStorageCutoff
	ctx := r.Context()
	closedName, found := "", false
	for _, c := range h.legacyCutoffCandidates(r, p) {
		at, name, err := db.SiteCreatedAt(ctx, h.database, c.id)
		if err != nil {
			continue
		}
		if at.Before(cutoff) {
			return "", false
		}
		if !found {
			closedName, found = name, true
			if closedName == "" {
				closedName = c.name
			}
		}
	}
	return closedName, found
}

type legacyCutoffCandidate struct {
	id, name string
}

func (h *SiteHandler) legacyCutoffCandidates(r *http.Request, p legacyStoragePath) []legacyCutoffCandidate {
	ctx := r.Context()
	var out []legacyCutoffCandidate
	seen := map[string]bool{}
	add := func(id, name string) {
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		out = append(out, legacyCutoffCandidate{id: id, name: name})
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
		u, err := db.GetUserByHandleOrAlias(ctx, h.database, p.handle)
		if err != nil {
			return out
		}
		if s, err := db.GetSiteByUser(ctx, h.database, u.ID, p.site); err == nil {
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
