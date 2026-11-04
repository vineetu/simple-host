package handler

import (
	"context"
	"database/sql"
	"time"

	"github.com/vsriram/simple-host/internal/app/accounts"
	"github.com/vsriram/simple-host/internal/edition"
	"github.com/vsriram/simple-host/internal/mcp"
	"github.com/vsriram/simple-host/internal/model"
)

// limitSet is the operational limits an instance was built with: a value
// copy of the edition's edition.Limits (edition.Edition.Limits()), held by
// every handler that needs to read or state one (SiteHandler, UserHandler,
// OAuthHandler, ConnectorHandler, HackHandler, APIMetrics). It is embedded,
// not a named field, so its methods are promoted: a call site that used to
// read the package-global config.Active() now reads its own handler's own
// copy (h.authTokenTTL()) instead, with no shared global and no setter.
type limitSet struct {
	limits edition.Limits
}

// newLimitSet copies l into a limitSet. Construct one from ed.Limits() when
// building a handler.
func newLimitSet(l edition.Limits) limitSet { return limitSet{limits: l} }

// mcpLimits maps the instance snapshot to the connector’s presentation limits.
func mcpLimits(l edition.Limits) mcp.Limits {
	return mcp.Limits{
		DeletedRetention:     l.DeletedRetention,
		PreviewLinkTTL:       l.PreviewLinkTTL,
		ExportLinkTTL:        l.ExportLinkTTL,
		IdleAfter:            l.IdleAfter,
		IdleGrace:            l.IdleGrace,
		DomainUnprovenTTL:    l.DomainUnprovenTTL,
		DomainLapseWarnAfter: l.DomainLapseWarnAfter,
		DomainLapseAfter:     l.DomainLapseAfter,
		UndoDays:             time.Duration(l.SavedData.UndoDays) * 24 * time.Hour,
		FamilyUnprovenTTL:    l.FamilyUnprovenTTL,
	}
}

// The operational times and limits the handlers use, read from the snapshot
// each handler was built with, so a knob set at startup — or an edition
// built with overrides in a test — is what every caller sees. Each defaults
// to the value the constant it replaced had.

// authTokenTTL is how long an emailed sign-in or email-change code works.
func (s limitSet) authTokenTTL() time.Duration { return s.limits.SigninCodeTTL }

// handleRenameEvery: after publishing, an account may move to a new handle
// once in this long. Changes before anything is published are free.
func (s limitSet) handleRenameEvery() time.Duration { return s.limits.HandleRenameEvery }

// maxSitesPerUser caps how many sites a single non-admin account may create, so
// a self-registered user can't fill the shared disk with sites. Admins are
// exempt.
func (s limitSet) maxSitesPerUser() int { return s.limits.MaxSitesPerAccount }

// maxSitesFor is the sites this account may hold: its MAX_SITES_OVERRIDES
// entry, matched against its current handle and then its earlier ones (so a
// handle change keeps the override), else MAX_SITES_PER_ACCOUNT. Resolved on
// every call, never cached. A failed alias read falls back to the current
// handle alone.
func (s limitSet) maxSitesFor(ctx context.Context, database *sql.DB, user *model.User) int {
	l := s.limits
	if l.MaxSitesOverrides == "" || user == nil {
		return l.MaxSitesPerAccount
	}
	return l.MaxSitesFor(accountHandles(ctx, database, user)...)
}

// siteLimitFor is the largest site this account may deploy, in bytes: its
// MAX_ARCHIVE_MB_OVERRIDES entry (matched like MAX_SITES_OVERRIDES, against
// its current handle and then its earlier ones), else MAX_ARCHIVE_MB. It caps
// the upload body and the unpacked files alike. Admins are not exempt (they
// never were). Only new uploads are held to it: a site already larger stays
// up and keeps its versions.
//
// override says an entry matched. Without one the unpacked files stay under
// the instance's own extractor caps (tarball.Extract), which are not derived
// from MAX_ARCHIVE_MB when it is unset (500 MB total, 100 MB a file).
func (s limitSet) siteLimitFor(ctx context.Context, database *sql.DB, user *model.User) (limit int64, override bool) {
	l := s.limits
	if l.MaxArchiveOverrides == "" || user == nil {
		return uploadLimits.Body, false
	}
	if mb, ok := l.ArchiveOverrideMB(accountHandles(ctx, database, user)...); ok {
		return int64(mb) << 20, true
	}
	return uploadLimits.Body, false
}

// statedSiteMB is a per-site limit as it is reported: in MB, and never above
// what the extractor will take (500 MB), which is where a
// MAX_ARCHIVE_MB above it really stops.
func statedSiteMB(limit int64) int {
	if mb := int(limit >> 20); mb < 500 {
		return mb
	}
	return 500
}

// siteLimitMBOf is siteLimitFor as reported, with the account's earlier
// handles already read (the admin list reads them all at once).
func (s limitSet) siteLimitMBOf(u model.User, aliases []string) int {
	if mb, ok := s.limits.ArchiveOverrideMB(append([]string{u.Handle.String}, aliases...)...); ok {
		return statedSiteMB(int64(mb) << 20)
	}
	return statedSiteMB(uploadLimits.Body)
}

// largestSiteLimit is the largest per-site cap any account has (MAX_ARCHIVE_MB
// or an override above it), for caps that sit in front of the account being
// known, like the connector's message size.
func (s limitSet) largestSiteLimit() int64 {
	max := uploadLimits.Body
	for _, mb := range s.limits.ArchiveOverrides() {
		if b := int64(mb) << 20; b > max {
			max = b
		}
	}
	return max
}

// accountHandles is the account's current handle and then its earlier ones,
// which is how the per-account overrides are matched. A failed alias read
// falls back to the current handle alone.
func accountHandles(ctx context.Context, database *sql.DB, user *model.User) []string {
	return accounts.New(database).SiteAccountHandles(ctx, user)
}

func (s limitSet) previewLinkTTL() time.Duration { return s.limits.PreviewLinkTTL }
func (s limitSet) exportLinkTTL() time.Duration  { return s.limits.ExportLinkTTL }

// visitorSessionTTL is a visitor sign-in's absolute lifetime (and the cookie's
// max age); visitorSessionIdle is how long it lasts unused (slid on use,
// server-side).
func (s limitSet) visitorSessionTTL() time.Duration  { return s.limits.VisitorSessionTTL }
func (s limitSet) visitorSessionIdle() time.Duration { return s.limits.VisitorSessionIdle }
func (s limitSet) visitorCookieMaxAge() int          { return int(s.visitorSessionTTL() / time.Second) }

func (s limitSet) oauthAccessTTL() time.Duration  { return s.limits.OAuthAccessTTL }
func (s limitSet) oauthRefreshTTL() time.Duration { return s.limits.OAuthRefreshTTL }

func (s limitSet) domainCheckInterval() time.Duration { return s.limits.DomainCheckInterval }

// A verified domain that keeps failing its checks: its owner is emailed after
// domainLapseWarnAfter, and after domainLapseAfter it stops being the site's
// home (its verification is cleared).
func (s limitSet) domainLapseWarnAfter() time.Duration { return s.limits.DomainLapseWarnAfter }
func (s limitSet) domainLapseAfter() time.Duration     { return s.limits.DomainLapseAfter }

// eventTTL is how long a claimed event name lives before the sweep removes it;
// maxClaimsPerAccount caps how many one account can hold at once.
func (s limitSet) eventTTL() time.Duration  { return s.limits.EventTTL }
func (s limitSet) maxClaimsPerAccount() int { return s.limits.EventMaxClaims }

func (s limitSet) idleAfter() time.Duration { return s.limits.IdleAfter }
func (s limitSet) idleGrace() time.Duration { return s.limits.IdleGrace }
func (s limitSet) idleReplyTo() string      { return s.limits.IdleReplyTo }

func (s limitSet) metricsRetentionDays() int { return s.limits.APIMetricsRetention }

// newRateLimiterFor builds a limiter from a configured rate.
func newRateLimiterFor(r struct {
	Burst int
	Every time.Duration
}) *rateLimiter {
	return newRateLimiter(float64(r.Burst), 1/r.Every.Seconds())
}

// maxSitesOf is maxSitesFor with the account's earlier handles already read
// (the admin list reads them all at once); nil for an admin, who has no limit.
func (s limitSet) maxSitesOf(u model.User, aliases []string) any {
	if u.IsAdmin {
		return nil
	}
	return s.limits.MaxSitesFor(append([]string{u.Handle.String}, aliases...)...)
}
