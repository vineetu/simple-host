package handler

import (
	"context"
	"database/sql"
	"time"

	"github.com/vsriram/simple-host/internal/config"
	db "github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/mcp"
	"github.com/vsriram/simple-host/internal/tarball"
)

// ApplyLimits makes l the limits in force everywhere: this package reads them
// from config, and the leaf packages that enforce or state some of them (db,
// mcp, tarball) get their share, since they may not import config themselves.
// main calls it once, before serving; tests call it to try a changed knob.
// The sign-in email's wording and the analytics retention are handed to the
// mailer and the ingester by main, which builds those.
func ApplyLimits(l config.Limits) {
	config.SetActive(l)
	db.SetLimits(db.Limits{
		MaxAccountKeys:       l.MaxKeysPerAccount,
		KeyIdleExpiry:        l.KeyIdleExpiry,
		EmailChangeUndoTTL:   l.EmailChangeUndoTTL,
		DeletedRetention:     l.DeletedRetention,
		UnprovenDomainTTL:    l.DomainUnprovenTTL,
		UnprovenDomainMaxAge: l.DomainUnprovenMaxAge,
		DomainCertDailyCap:   l.DomainCertsDaily,
		VisitorSessionIdle:   l.VisitorSessionIdle,
		OAuthUnusedClientAge: l.OAuthUnusedClientAge,
	})
	mcp.SetLimits(mcp.Limits{
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
	})
	tarball.SetMaxEntries(l.MaxFilesPerSite)
	instanceLimits = newLimitsRewriter(l)
}

// The operational times and limits the handlers use, read from the one config
// place (internal/config/limits.go) each time, so a knob set at startup — or
// changed by a test — is what every caller sees. Each defaults to the value
// the constant it replaced had.

// authTokenTTL is how long an emailed sign-in or email-change code works.
func authTokenTTL() time.Duration { return config.Active().SigninCodeTTL }

// handleRenameEvery: after publishing, an account may move to a new handle
// once in this long. Changes before anything is published are free.
func handleRenameEvery() time.Duration { return config.Active().HandleRenameEvery }

// maxSitesPerUser caps how many sites a single non-admin account may create, so
// a self-registered user can't fill the shared disk with sites. Admins are
// exempt.
func maxSitesPerUser() int { return config.Active().MaxSitesPerAccount }

// maxSitesFor is the sites this account may hold: its MAX_SITES_OVERRIDES
// entry, matched against its current handle and then its earlier ones (so a
// handle change keeps the override), else MAX_SITES_PER_ACCOUNT. Resolved on
// every call, never cached. A failed alias read falls back to the current
// handle alone.
func maxSitesFor(ctx context.Context, database *sql.DB, user *db.User) int {
	l := config.Active()
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
func siteLimitFor(ctx context.Context, database *sql.DB, user *db.User) (limit int64, override bool) {
	l := config.Active()
	if l.MaxArchiveOverrides == "" || user == nil {
		return maxSiteArchiveSize, false
	}
	if mb, ok := l.ArchiveOverrideMB(accountHandles(ctx, database, user)...); ok {
		return int64(mb) << 20, true
	}
	return maxSiteArchiveSize, false
}

// statedSiteMB is a per-site limit as it is reported: in MB, and never above
// what the extractor will take (config.MaxArchiveCeilingMB), which is where a
// MAX_ARCHIVE_MB above it really stops.
func statedSiteMB(limit int64) int {
	if mb := int(limit >> 20); mb < config.MaxArchiveCeilingMB {
		return mb
	}
	return config.MaxArchiveCeilingMB
}

// siteLimitMBOf is siteLimitFor as reported, with the account's earlier
// handles already read (the admin list reads them all at once).
func siteLimitMBOf(u db.User, aliases []string) int {
	if mb, ok := config.Active().ArchiveOverrideMB(append([]string{u.Handle.String}, aliases...)...); ok {
		return statedSiteMB(int64(mb) << 20)
	}
	return statedSiteMB(maxSiteArchiveSize)
}

// largestSiteLimit is the largest per-site cap any account has (MAX_ARCHIVE_MB
// or an override above it), for caps that sit in front of the account being
// known, like the connector's message size.
func largestSiteLimit() int64 {
	max := maxSiteArchiveSize
	for _, mb := range config.Active().ArchiveOverrides() {
		if b := int64(mb) << 20; b > max {
			max = b
		}
	}
	return max
}

// accountHandles is the account's current handle and then its earlier ones,
// which is how the per-account overrides are matched. A failed alias read
// falls back to the current handle alone.
func accountHandles(ctx context.Context, database *sql.DB, user *db.User) []string {
	handles := []string{user.Handle.String}
	if aliases, err := db.ListHandleAliases(ctx, database, user.ID); err == nil {
		handles = append(handles, aliases...)
	}
	return handles
}

func previewLinkTTL() time.Duration { return config.Active().PreviewLinkTTL }
func exportLinkTTL() time.Duration  { return config.Active().ExportLinkTTL }

// visitorSessionTTL is a visitor sign-in's absolute lifetime (and the cookie's
// max age); visitorSessionIdle is how long it lasts unused (slid on use,
// server-side).
func visitorSessionTTL() time.Duration  { return config.Active().VisitorSessionTTL }
func visitorSessionIdle() time.Duration { return config.Active().VisitorSessionIdle }
func visitorCookieMaxAge() int          { return int(visitorSessionTTL() / time.Second) }

func oauthAccessTTL() time.Duration  { return config.Active().OAuthAccessTTL }
func oauthRefreshTTL() time.Duration { return config.Active().OAuthRefreshTTL }

func domainCheckInterval() time.Duration { return config.Active().DomainCheckInterval }

// A verified domain that keeps failing its checks: its owner is emailed after
// domainLapseWarnAfter, and after domainLapseAfter it stops being the site's
// home (its verification is cleared).
func domainLapseWarnAfter() time.Duration { return config.Active().DomainLapseWarnAfter }
func domainLapseAfter() time.Duration     { return config.Active().DomainLapseAfter }

// eventTTL is how long a claimed event name lives before the sweep removes it;
// maxClaimsPerAccount caps how many one account can hold at once.
func eventTTL() time.Duration  { return config.Active().EventTTL }
func maxClaimsPerAccount() int { return config.Active().EventMaxClaims }

func idleAfter() time.Duration { return config.Active().IdleAfter }
func idleGrace() time.Duration { return config.Active().IdleGrace }
func idleReplyTo() string      { return config.Active().IdleReplyTo }

func metricsRetentionDays() int { return config.Active().APIMetricsRetention }

// newRateLimiterFor builds a limiter from a configured rate.
func newRateLimiterFor(r config.Rate) *rateLimiter {
	return newRateLimiter(float64(r.Burst), r.PerSecond())
}

// maxSitesOf is maxSitesFor with the account's earlier handles already read
// (the admin list reads them all at once); nil for an admin, who has no limit.
func maxSitesOf(u db.User, aliases []string) any {
	if u.IsAdmin {
		return nil
	}
	return config.Active().MaxSitesFor(append([]string{u.Handle.String}, aliases...)...)
}
