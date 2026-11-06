package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/vsriram/simple-host/internal/mcp"
)

func (h *SiteHandler) visitorCollectionRequest(r *http.Request, args map[string]any, action, method string, value any) (any, error) {
	name := r.PathValue("coll")
	siteID, err := h.resolveSiteIDScoped(r, r.PathValue("sitename"))
	if err != nil {
		return nil, mcp.CodedError("site_not_found", "site_not_found: that site no longer exists; ask the person for the current address, or call visitor_read_site with no address to list sites shared with you")
	}
	sess, ok := h.internalVisitorSession(r, siteID)
	if !ok {
		return nil, fmt.Errorf("visitor pass does not cover this site")
	}
	set, err := h.legacySvc().VisitorCollectionSettings(r.Context(), siteID, name)
	if err != nil {
		return nil, err
	}
	r.URL.Path = "/v1/sites/" + r.PathValue("sitename") + "/collections/" + name
	var fn http.HandlerFunc
	switch action {
	case "read":
		fn = h.listCollection
		if set.Private && entriesLike(set) {
			fn = func(w http.ResponseWriter, r *http.Request) { h.listMine(w, r, r.PathValue("sitename"), name) }
		}
	case "add":
		fn = h.appendCollection
	default:
		id, err := visitorEntryID(args["id"])
		if err != nil {
			return nil, err
		}
		r.SetPathValue("id", strconv.FormatInt(id, 10))
		if !h.legacySvc().VisitorEntryOwned(r.Context(), siteID, name, id, sess.UserID, true) {
			return nil, fmt.Errorf("not_found: no entry of yours with that id")
		}
		fn = h.updateEntry
		if action == "delete" {
			fn = h.withdrawEntry
		}
	}
	var handler http.Handler = fn
	if action != "read" {
		handler = rateLimitByIP(h.stateLimiter, handler)
	}
	result, err := visitorInvoke(r, method, value, h.LegacyStorageCutoffGate(handler).ServeHTTP)
	if err != nil {
		return nil, err
	}
	// The browser strips author details; mark only entries belonging to this
	// caller, without adding another visitor's reference or account ID.
	var mark func(any)
	mark = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if raw, ok := x["id"].(json.Number); ok {
				id, _ := raw.Int64()
				owned, err := h.legacySvc().VisitorEntryOwnership(r.Context(), siteID, name, id, sess.UserID)
				if err == nil {
					x["mine"] = owned
				}
			}
			if items, ok := x["items"].([]any); ok {
				for _, it := range items {
					mark(it)
				}
			}
		case []any:
			for _, it := range x {
				mark(it)
			}
		}
	}
	mark(result)
	return result, nil
}
