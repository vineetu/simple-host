package handler

import (
	"context"
	"github.com/vsriram/simple-host/internal/app/legacy"
	db "github.com/vsriram/simple-host/internal/db"
	"net/http"
	"strings"
)

func (h *SiteHandler) legacySvc() *legacy.Adapter {
	return legacy.New(h.database, h.legacyStorageCutoff, legacy.Deps{MaxEntries: h.savedData.EntriesMax, WatchItemKB: h.savedData.WatchItemKB, MaxBytes: h.siteMaxBytes(), NewListMetric: watchNewList, LargeItemMetric: watchItemLarge})
}

// legacyRequestSvc supplies the request-specific named-site resolver.
func (h *SiteHandler) legacyRequestSvc(r *http.Request) *legacy.Adapter {
	return h.legacyCollectionRequestSvc(r, nil)
}

func (h *SiteHandler) legacyCollectionRequestSvc(r *http.Request, origin func(string) bool) *legacy.Adapter {
	return legacy.New(h.database, h.legacyStorageCutoff, h.legacyCollectionRequestDeps(r, origin))
}

func (h *SiteHandler) legacyCollectionRequestDeps(r *http.Request, origin func(string) bool) legacy.Deps {
	return legacy.Deps{
		AdminAPIKey: h.adminAPIKey, KeyIdleExpiry: h.limits.KeyIdleExpiry,
		ResolveNamedSite:          func(_ context.Context, siteName string) (string, error) { return h.resolveSiteID(r, siteName) },
		ResolveWriteSite:          func(_ context.Context, siteName string) (string, error) { return h.resolveWriteSiteID(r, siteName) },
		AuthorizeCollectionOrigin: origin,
	}
}

type collectionRateHeaders http.Header

func (h collectionRateHeaders) Header() http.Header       { return http.Header(h) }
func (collectionRateHeaders) Write(p []byte) (int, error) { return len(p), nil }
func (collectionRateHeaders) WriteHeader(int)             {}

func (h *SiteHandler) legacyCollectionReadSvc(w http.ResponseWriter, r *http.Request, rateHeaders http.Header) *legacy.Adapter {
	svc := h.legacyCollectionRequestDeps(r, func(name string) bool { return h.authorizeStateOrigin(w, r, name) })
	svc.CollectionReadAllowed = func(siteID string, _ bool) bool {
		return h.readLimiter == nil || h.readLimiter.allowHTTP(collectionRateHeaders(rateHeaders), h.readBucket(r, siteID))
	}
	svc.CollectionPrivateRead = func(siteID string, owner bool) error {
		if owner {
			return nil
		}
		if strings.EqualFold(requestHostName(r), h.contentHost) {
			return &legacy.Error{Code: "private_not_found"}
		}
		info, here, err := h.onOwnDomain(r, siteID)
		if err != nil {
			return err
		}
		if !here {
			return &legacy.Error{Code: "private_not_found"}
		}
		sess, ok := h.strictVisitorSession(r, siteID)
		if !ok || sess.UserID != info.OwnerID || r.Header.Get(visitorPassHeader) != "" {
			return &legacy.Error{Code: "private_not_found"}
		}
		_ = db.TouchVisitorSession(r.Context(), h.database, sess.ID, h.limits.VisitorSessionIdle)
		return nil
	}
	svc.CollectionAuthorView = func(siteID string, owner bool) bool {
		return owner || h.ownerBrowserView(r, siteID)
	}
	return legacy.New(h.database, h.legacyStorageCutoff, svc)
}
