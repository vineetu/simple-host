package data

import (
	"context"
	"net/http"
	"strings"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
)

// Route is a data route: it runs once Access has resolved the site and the
// caller (AccessHandler.StorageCaller). A feature's route is a plain function
// of the handler; a core one is a method expression, (*Handler).Name.
type Route func(*Handler, http.ResponseWriter, *http.Request, Call)

// StorageRouteFor puts Access's caller resolution (caller, which is
// AccessHandler.StorageCaller) in front of a data route.
func (h *Handler) StorageRouteFor(caller func(http.Handler) http.Handler, fn Route) http.Handler {
	return caller(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := CallFrom(r.Context())
		c.RuntimeDir = h.StorageRuntimeDir(c)
		fn(h, w, r, c)
	}))
}

func StorageOwner(w http.ResponseWriter, c Call) bool {
	if !c.Owner {
		httpx.WriteError(w, 403, "forbidden", "owner credential required")
	}
	return c.Owner
}

// storageResourceFor resolves the resource the route names and admits a
// visitor's request to it.
func (h *Handler) StorageResourceFor(w http.ResponseWriter, r *http.Request, c Call, kind string) (Call, bool) {
	c, err := h.StorageSvc().ResourceCall(r.Context(), c, r.PathValue("name"), kind)
	if err != nil {
		WriteStorageError(w, err, 0)
		return c, false
	}
	return c, c.Owner || h.admitVisitor(w, r, &c)
}

// admitVisitor applies a visitor's write rate limits (per address, then per
// signed-in visitor) and runs the spam check.
func (h *Handler) admitVisitor(w http.ResponseWriter, r *http.Request, c *Call) bool {
	write := storageVisitorWrite(r)
	if write && !h.StorageIPLimiter.Allow(c.SiteID+"/"+httpx.ClientIP(r)) ||
		write && c.VisitorID != "" && !h.StorageVisitorLimiter.Allow(c.SiteID+"/"+c.VisitorID) {
		httpx.TooManyRequests(w)
		return false
	}
	return h.storageSpamCheck(w, r, c)
}

// POST query and download-link routes only read; method alone is insufficient.
func storageVisitorWrite(r *http.Request) bool {
	return r.Method == http.MethodPut || r.Method == http.MethodDelete || r.Method == http.MethodPatch ||
		r.Method == http.MethodPost && !strings.HasSuffix(r.URL.Path, "/query") && !strings.HasSuffix(r.URL.Path, "/download-link")
}

func (h *Handler) ListStorageResourceRows(ctx context.Context, siteID string) ([]model.Resource, error) {
	return h.StorageSvc().ListResourceRecords(ctx, siteID)
}

func StoragePage(r *http.Request) (string, string, int, bool) {
	q := r.URL.Query()
	limit, ok := httpx.Limit(q, 100, 500)
	return q.Get("prefix"), q.Get("after"), limit, ok
}
