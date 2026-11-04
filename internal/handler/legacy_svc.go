package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/vsriram/simple-host/internal/app/legacy"
	db "github.com/vsriram/simple-host/internal/db"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// signalLegacyBodyTooLarge preserves net/http's oversized-request connection behavior.
func signalLegacyBodyTooLarge(w http.ResponseWriter) {
	_, _ = io.ReadAll(http.MaxBytesReader(w, io.NopCloser(strings.NewReader("x")), 0))
}

func (h *SiteHandler) legacySvc() *legacy.Adapter {
	return legacy.New(h.database, h.legacyStorageCutoff, legacy.Deps{CSVSafe: csvSafe, MaxEntries: func() int { return h.savedData.EntriesMax }, WatchItemKB: func() int { return h.savedData.WatchItemKB }, MaxBytes: h.siteMaxBytes, NewListMetric: watchNewList, LargeItemMetric: watchItemLarge, CollectionJSONStorable: jsonStorable})
}

// legacyRequestSvc supplies the request-specific named-site resolver.
func (h *SiteHandler) legacyRequestSvc(r *http.Request) *legacy.Adapter {
	return h.legacyCollectionRequestSvc(r)
}

func (h *SiteHandler) legacyCollectionRequestSvc(r *http.Request) *legacy.Adapter {
	return legacy.New(h.database, h.legacyStorageCutoff, h.legacyCollectionRequestDeps(r))
}

func (h *SiteHandler) legacyCollectionRequestDeps(r *http.Request) legacy.Deps {
	return legacy.Deps{
		AdminAPIKey: h.adminAPIKey, KeyIdleExpiry: h.limits.KeyIdleExpiry,
		ResolveNamedSite: func(_ context.Context, siteName string) (string, error) { return h.resolveSiteID(r, siteName) },
		ResolveWriteSite: func(_ context.Context, siteName string) (string, error) { return h.resolveWriteSiteID(r, siteName) },
	}
}

// legacyCollectionReadSvc supplies request-bound facts without writing the response.
func (h *SiteHandler) legacyCollectionReadSvc(r *http.Request) *legacy.Adapter {
	deps := h.legacyCollectionRequestDeps(r)
	deps.VisitorSessionIdle = h.limits.VisitorSessionIdle
	deps.CollectionOwnerDomain = func(_ context.Context, siteID string) (string, bool, error) {
		info, here, err := h.onOwnDomain(r, siteID)
		return info.OwnerID, here, err
	}
	deps.CollectionBrowserSession = func(_ context.Context, siteID string) legacy.CollectionBrowserSession {
		sess, ok := h.strictVisitorSession(r, siteID)
		return legacy.CollectionBrowserSession{UserID: sess.UserID, ID: sess.ID, OK: ok}
	}
	deps.CollectionOrigin = h.collectionOrigin(r)
	deps.CollectionReadLimit = func(siteID string) (bool, map[string][]string) {
		if h.readLimiter == nil {
			return true, nil
		}
		return collectionRateLimit(h.readLimiter, h.readBucket(r, siteID))
	}
	return legacy.New(h.database, h.legacyStorageCutoff, deps)
}

// collectionOrigin derives CORS headers without writing the response.
func (h *SiteHandler) collectionOrigin(r *http.Request) func(string) (bool, map[string][]string) {
	return func(siteName string) (bool, map[string][]string) {
		siteID, err := h.resolveWriteSiteID(r, siteName)
		if err != nil {
			return false, nil
		}
		origin := requestOrigin(r)
		if origin == "" {
			return false, nil
		}
		parsed, err := url.Parse(origin)
		if err != nil || parsed.Host == "" {
			return false, nil
		}
		if !(strings.EqualFold(parsed.Host, h.contentHost) && h.sharedHost(requestHostName(r))) &&
			!h.originIsSiteHostID(r.Context(), siteID, parsed.Host) &&
			!h.originIsFamilyHostID(r.Context(), siteID, parsed) &&
			!h.originIsPersonHostID(r.Context(), siteID, parsed.Host) &&
			!h.originIsBoundDomainID(r.Context(), siteID, parsed.Host) &&
			!(h.originAllowedForSiteID(r.Context(), siteID, origin) && !strings.EqualFold(parsed.Host, h.contentHost)) {
			return false, nil
		}
		return true, map[string][]string{"Access-Control-Allow-Origin": {origin}, "Access-Control-Allow-Credentials": {"true"}, "Access-Control-Expose-Headers": {"ETag"}, "Vary": {"Origin"}}
	}
}

// collectionRateLimit returns the same headers as the shared HTTP limiter.
func collectionRateLimit(limiter *rateLimiter, bucket string) (bool, map[string][]string) {
	if limiter == nil {
		return true, nil
	}
	allowed, tokens, next := limiter.AllowState(bucket)
	remaining, reset := int(math.Floor(tokens)), int(math.Ceil(next))
	capacity := strconv.FormatFloat(limiter.Capacity, 'f', -1, 64)
	policy := `"token-bucket";q=` + capacity
	if limiter.Rate > 0 {
		policy += ";w=" + strconv.FormatFloat(math.Ceil(limiter.Capacity/limiter.Rate), 'f', 0, 64)
	}
	policy += `;refill="` + strconv.FormatFloat(limiter.Rate, 'f', -1, 64) + `"`
	headers := map[string][]string{"Ratelimit-Policy": {policy}, "Ratelimit": {fmt.Sprintf(`"token-bucket";r=%d;t=%d`, remaining, reset)}, "Ratelimit-Limit": {capacity}, "Ratelimit-Remaining": {strconv.Itoa(remaining)}, "Ratelimit-Reset": {strconv.Itoa(reset)}}
	if !allowed {
		headers["Retry-After"] = []string{strconv.Itoa(reset)}
	}
	return allowed, headers
}

// legacyCollectionAppendSvc supplies request facts for a public append.
func (h *SiteHandler) legacyCollectionAppendSvc(r *http.Request) *legacy.Adapter {
	deps := h.legacyCollectionRequestDeps(r)
	deps.MaxEntries, deps.WatchItemKB, deps.MaxBytes = func() int { return h.savedData.EntriesMax }, func() int { return h.savedData.WatchItemKB }, h.siteMaxBytes
	deps.NewListMetric, deps.LargeItemMetric, deps.CollectionJSONStorable = watchNewList, watchItemLarge, jsonStorable
	deps.CollectionOrigin = h.collectionOrigin(r)
	deps.KeyIdleExpiry, deps.VisitorSessionIdle = h.limits.KeyIdleExpiry, h.limits.VisitorSessionIdle
	deps.CollectionOwnerKeyWrite = func(ownerID string) bool { return h.ownerKeyWrite(r, ownerID) }
	deps.CollectionDomainElsewhere = func(siteID string) (string, bool) { return h.livesOnDomainElsewhere(r, siteID) }
	deps.CollectionSharedDomain = func(ctx context.Context, siteID string) (string, bool) {
		info, ok, _ := db.GetSiteDomainInfo(ctx, h.database, siteID)
		if !ok || info.Domain == "" {
			return "", false
		}
		if own, has, _ := h.siteOwnAddress(ctx, siteID); has {
			info.Domain = own.Domain
		}
		return info.Domain, true
	}
	deps.CollectionKeyActor = func(ownerID string) db.Actor { return h.keyActor(r, ownerID) }
	deps.CollectionInternalSession = func(siteID string) (db.VisitorSession, bool) { return h.internalVisitorSession(r, siteID) }
	deps.CollectionResolveKey = h.resolveWriterKey
	deps.CollectionSessionValid = func(sess db.VisitorSession, siteID string) bool { return h.sessionValidFor(r, sess, siteID) }
	deps.CollectionLogAnon = func(siteID, name, mode, outcome string) {
		h.logAnonWrite(r, siteID, name, writeRouteCollectionPost, strings.TrimSpace(r.PathValue("coll")), mode, outcome)
	}
	deps.CollectionAppendLimit = func() (bool, map[string][]string) { return collectionRateLimit(h.appendLimiter, clientIP(r)) }
	deps.BoardMax = func() int { return h.savedData.BoardMax }
	deps.CollectionOwnerBrowser = func(siteID string) bool { return h.ownerBrowserView(r, siteID) }
	deps.CollectionStrictSession = func(siteID string) legacy.CollectionBrowserSession {
		sess, ok := h.strictVisitorSession(r, siteID)
		return legacy.CollectionBrowserSession{UserID: sess.UserID, ID: sess.ID, OK: ok}
	}
	deps.CollectionPrivateHome = func(siteID string) (legacy.CollectionPrivateHome, bool, error) {
		info, here, err := h.onOwnDomain(r, siteID)
		return legacy.CollectionPrivateHome{Host: info.Host, OwnerID: info.OwnerID, IsDomain: info.IsDomain}, here, err
	}
	deps.CollectionPrivateWrongHost = func() bool {
		host := requestHostName(r)
		return strings.EqualFold(host, h.contentHost) || h.isSiteHostName(host) || h.isPersonHost(r.Context(), host) || h.isFamilyHostName(host)
	}
	deps.CollectionBoardLimit = func(userID string) bool { return h.boardLimiter == nil || h.boardLimiter.allow("u:"+userID) }
	return legacy.New(h.database, h.legacyStorageCutoff, deps)
}

// legacyPersonalPutSvc supplies request-specific dependencies for personal writes.
func (h *SiteHandler) legacyPersonalPutDeps(r *http.Request) legacy.Deps {
	deps := h.legacyCollectionRequestDeps(r)
	deps.PersonalSiteMaxBytes, deps.PersonalPeopleMax = h.siteMaxBytes, func() int { return h.savedData.PersonalPeopleMax }
	deps.VisitorSessionIdle, deps.CollectionJSONStorable = h.limits.VisitorSessionIdle, jsonStorable
	deps.BoundPersonalHistory = func(_ context.Context, siteID string) { h.boundHistory(r, siteID) }
	deps.CollectionOrigin = h.collectionOrigin(r)
	deps.CollectionStrictSession = func(siteID string) legacy.CollectionBrowserSession {
		sess, ok := h.strictVisitorSession(r, siteID)
		return legacy.CollectionBrowserSession{UserID: sess.UserID, ID: sess.ID, OK: ok}
	}
	deps.CollectionReadLimit = func(siteID string) (bool, map[string][]string) {
		if h.readLimiter == nil {
			return true, nil
		}
		return collectionRateLimit(h.readLimiter, h.readBucket(r, siteID))
	}
	deps.CollectionAppendLimit = func() (bool, map[string][]string) { return collectionRateLimit(h.appendLimiter, clientIP(r)) }
	return deps
}

func (h *SiteHandler) legacyPersonalPutSvc(r *http.Request) *legacy.Adapter {
	return legacy.New(h.database, h.legacyStorageCutoff, h.legacyPersonalPutDeps(r))
}

// legacyPersonalPatchSvc supplies pure patch transformation for a personal write.
func (h *SiteHandler) legacyPersonalPatchSvc(r *http.Request, maxBytes *int64) *legacy.Adapter {
	deps := h.legacyPersonalPutDeps(r)
	deps.PersonalPreparePatch = func(body []byte) (func(json.RawMessage) (json.RawMessage, error), error) {
		var req struct {
			Ops []stateOp `json:"ops"`
		}
		dec := json.NewDecoder(bytes.NewReader(body))
		dec.UseNumber()
		var err error
		if bytes.HasPrefix(bytes.TrimSpace(body), []byte("[")) {
			err = dec.Decode(&req.Ops)
		} else {
			err = dec.Decode(&req)
		}
		if err != nil {
			return nil, &legacy.PersonalPatchError{Kind: "bad_request", Message: `send {"ops": [{"op": "set", "path": "theme", "value": "dark"}]}`}
		}
		if len(req.Ops) == 0 || len(req.Ops) > maxStateOps {
			return nil, &legacy.PersonalPatchError{Kind: "bad_request", Message: fmt.Sprintf("ops is required: 1 to %d of them", maxStateOps)}
		}
		return func(cur json.RawMessage) (json.RawMessage, error) {
			root, err := stateRootObject(cur)
			if err != nil {
				return nil, &legacy.PersonalPatchError{Kind: "conflict", Message: "this record is not a JSON object; replace it whole with PUT", WireCode: "not_an_object"}
			}
			if err := applyStateOps(root, req.Ops); err != nil {
				return nil, &legacy.PersonalPatchError{Kind: "bad_request", Message: err.Error()}
			}
			next, err := json.Marshal(root)
			if err != nil {
				return nil, err
			}
			if int64(len(next)) > *maxBytes && len(next) > len(cur) {
				rep := h.personalTooLarge()
				return nil, &legacy.PersonalPatchError{Kind: "too_large", Message: rep.msg, WireCode: rep.code}
			}
			return next, nil
		}, nil
	}
	return legacy.New(h.database, h.legacyStorageCutoff, deps)
}
