package handler

import (
	"strconv"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/config"
)

// The pages, llms.txt, the OpenAPI spec and the skills state some of the
// operational limits in words ("Recently deleted keeps it for 7 days"). They
// are written with today's values, which is what simple-host.app runs. An
// install that changes a knob (internal/config/limits.go) must not go on
// promising the old value, so every such sentence is listed here with how to
// say it from the settings, and served text is rewritten through the list —
// the same serve-time approach as instanceHosts for hostnames.
//
// Only phrases whose knob differs from its default are rewritten, so with
// every knob at its default nothing is (instanceLimits is nil) and the served
// bytes are exactly the files. TestLimitPhrasesAppearInServedText fails when a
// phrase no longer occurs in any served file, which is how an edit to the copy
// that breaks the match is caught. Go code that states a limit (emails, MCP
// descriptions, error messages) formats it from the settings directly and is
// not listed here.

// limitPhrase is one sentence fragment as written in the files (the default
// wording) and how to say it for a given set of limits.
type limitPhrase struct {
	text string
	// env names the knob(s) the phrase states, for the tests.
	env []string
	say func(l *config.Limits) string
}

func limitSpan(d time.Duration) string    { return config.Span(d) }
func limitSpanAdj(d time.Duration) string { return config.SpanAdj(d) }
func limitNum(n int) string               { return strconv.Itoa(n) }
func limitDays(n int) string              { return config.Count(n, "day") }

// commas prints 50000 as 50,000.
func limitCommas(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// perEvery words a rate's refill the way architecture.html does: "+1 per 10 s".
func perEvery(r config.Rate) string {
	if r.Every%time.Second == 0 && r.Every < time.Minute {
		return "+1 per " + limitNum(int(r.Every/time.Second)) + " s"
	}
	return "+1 per " + r.Every.String()
}

func phrase(text string, env []string, say func(l *config.Limits) string) limitPhrase {
	return limitPhrase{text: text, env: env, say: say}
}

func knob(env string) []string { return []string{env} }

// limitPhrases is every sentence in served text that states a knob's value.
// Keep each phrase on one line of its file (a line break inside it would not
// match the JSON copy of the spec) and long enough to be unambiguous.
var limitPhrases = []limitPhrase{
	// SIGNIN_CODE_TTL_MINUTES
	phrase("expires in 15 minutes", knob("SIGNIN_CODE_TTL_MINUTES"), func(l *config.Limits) string { return "expires in " + limitSpan(l.SigninCodeTTL) }),
	phrase("Expires in 15 minutes", knob("SIGNIN_CODE_TTL_MINUTES"), func(l *config.Limits) string { return "Expires in " + limitSpan(l.SigninCodeTTL) }),
	phrase("(15 minutes,", knob("SIGNIN_CODE_TTL_MINUTES"), func(l *config.Limits) string { return "(" + limitSpan(l.SigninCodeTTL) + "," }),
	phrase("valid for 15 minutes", knob("SIGNIN_CODE_TTL_MINUTES"), func(l *config.Limits) string { return "valid for " + limitSpan(l.SigninCodeTTL) }),
	phrase("after <b>15 minutes</b>", knob("SIGNIN_CODE_TTL_MINUTES"), func(l *config.Limits) string { return "after <b>" + limitSpan(l.SigninCodeTTL) + "</b>" }),
	phrase("Same 15-minute expiry", knob("SIGNIN_CODE_TTL_MINUTES"), func(l *config.Limits) string { return "Same " + limitSpanAdj(l.SigninCodeTTL) + " expiry" }),
	phrase("older than 15 minutes", knob("SIGNIN_CODE_TTL_MINUTES"), func(l *config.Limits) string { return "older than " + limitSpan(l.SigninCodeTTL) }),
	phrase("(15-minute code", knob("SIGNIN_CODE_TTL_MINUTES"), func(l *config.Limits) string { return "(" + limitSpanAdj(l.SigninCodeTTL) + " code" }),

	// MAX_KEYS_PER_ACCOUNT
	phrase("at most 50 keys", knob("MAX_KEYS_PER_ACCOUNT"), func(l *config.Limits) string { return "at most " + limitNum(l.MaxKeysPerAccount) + " keys" }),
	phrase("holds 50 keys", knob("MAX_KEYS_PER_ACCOUNT"), func(l *config.Limits) string { return "holds " + limitNum(l.MaxKeysPerAccount) + " keys" }),

	// HANDLE_RENAME_EVERY_DAYS
	phrase("once every 30 days", knob("HANDLE_RENAME_EVERY_DAYS"), func(l *config.Limits) string { return "once every " + limitSpan(l.HandleRenameEvery) }),
	phrase("the last 30 days after publishing", knob("HANDLE_RENAME_EVERY_DAYS"), func(l *config.Limits) string {
		return "the last " + limitSpan(l.HandleRenameEvery) + " after publishing"
	}),
	phrase("change it again after 30 days", knob("HANDLE_RENAME_EVERY_DAYS"), func(l *config.Limits) string { return "change it again after " + limitSpan(l.HandleRenameEvery) }),

	// EMAIL_CHANGE_UNDO_DAYS
	phrase("7-day undo link", knob("EMAIL_CHANGE_UNDO_DAYS"), func(l *config.Limits) string { return limitSpanAdj(l.EmailChangeUndoTTL) + " undo link" }),
	phrase("undo link valid for 7 days", knob("EMAIL_CHANGE_UNDO_DAYS"), func(l *config.Limits) string { return "undo link valid for " + limitSpan(l.EmailChangeUndoTTL) }),
	phrase("used or is older than 7 days", knob("EMAIL_CHANGE_UNDO_DAYS"), func(l *config.Limits) string { return "used or is older than " + limitSpan(l.EmailChangeUndoTTL) }),

	// MAX_SITES_PER_ACCOUNT
	phrase("Up to a hundred sites", knob("MAX_SITES_PER_ACCOUNT"), func(l *config.Limits) string { return "Up to " + limitCommas(l.MaxSitesPerAccount) + " sites" }),
	phrase("up to a hundred separate sites", knob("MAX_SITES_PER_ACCOUNT"), func(l *config.Limits) string { return "up to " + limitCommas(l.MaxSitesPerAccount) + " separate sites" }),
	phrase("<b>100 sites</b>", knob("MAX_SITES_PER_ACCOUNT"), func(l *config.Limits) string { return "<b>" + limitCommas(l.MaxSitesPerAccount) + " sites</b>" }),

	// MAX_FILES_PER_SITE
	phrase("50,000 entries", knob("MAX_FILES_PER_SITE"), func(l *config.Limits) string { return limitCommas(l.MaxFilesPerSite) + " entries" }),

	// PREVIEW_LINK_TTL_MINUTES
	phrase("has it, for one hour", knob("PREVIEW_LINK_TTL_MINUTES"), func(l *config.Limits) string { return "has it, for " + limitSpan(l.PreviewLinkTTL) }),
	phrase("has it for one hour", knob("PREVIEW_LINK_TTL_MINUTES"), func(l *config.Limits) string { return "has it for " + limitSpan(l.PreviewLinkTTL) }),
	phrase("stops working after an hour", knob("PREVIEW_LINK_TTL_MINUTES"), func(l *config.Limits) string { return "stops working after " + limitSpan(l.PreviewLinkTTL) }),
	phrase("with it, for one hour", knob("PREVIEW_LINK_TTL_MINUTES"), func(l *config.Limits) string { return "with it, for " + limitSpan(l.PreviewLinkTTL) }),
	phrase("yours for one hour", knob("PREVIEW_LINK_TTL_MINUTES"), func(l *config.Limits) string { return "yours for " + limitSpan(l.PreviewLinkTTL) }),

	// EXPORT_LINK_TTL_MINUTES
	phrase("link that works for 10 minutes", knob("EXPORT_LINK_TTL_MINUTES"), func(l *config.Limits) string { return "link that works for " + limitSpan(l.ExportLinkTTL) }),
	phrase("a 10-minute download link", knob("EXPORT_LINK_TTL_MINUTES"), func(l *config.Limits) string { return "a " + limitSpanAdj(l.ExportLinkTTL) + " download link" }),
	phrase("expires after 10 minutes", knob("EXPORT_LINK_TTL_MINUTES"), func(l *config.Limits) string { return "expires after " + limitSpan(l.ExportLinkTTL) }),
	phrase("site, for 10 minutes", knob("EXPORT_LINK_TTL_MINUTES"), func(l *config.Limits) string { return "site, for " + limitSpan(l.ExportLinkTTL) }),
	phrase("without a key for 10 minutes", knob("EXPORT_LINK_TTL_MINUTES"), func(l *config.Limits) string { return "without a key for " + limitSpan(l.ExportLinkTTL) }),

	// VISITOR_SESSION_DAYS, VISITOR_SESSION_IDLE_DAYS
	phrase("Session has 30-day absolute and 14-day idle lifetimes", []string{"VISITOR_SESSION_DAYS", "VISITOR_SESSION_IDLE_DAYS"}, func(l *config.Limits) string {
		return "Session has " + limitSpanAdj(l.VisitorSessionTTL) + " absolute and " + limitSpanAdj(l.VisitorSessionIdle) + " idle lifetimes"
	}),
	phrase("lasts up to 30 days and covers", knob("VISITOR_SESSION_DAYS"), func(l *config.Limits) string { return "lasts up to " + limitSpan(l.VisitorSessionTTL) + " and covers" }),
	phrase("for that one site (up to 30 days)", knob("VISITOR_SESSION_DAYS"), func(l *config.Limits) string {
		return "for that one site (up to " + limitSpan(l.VisitorSessionTTL) + ")"
	}),
	phrase("on sites:</strong> up to 30 days", knob("VISITOR_SESSION_DAYS"), func(l *config.Limits) string { return "on sites:</strong> up to " + limitSpan(l.VisitorSessionTTL) }),

	// OAUTH_ACCESS_TTL_MINUTES, OAUTH_REFRESH_TTL_DAYS
	phrase("access token that lasts one hour", knob("OAUTH_ACCESS_TTL_MINUTES"), func(l *config.Limits) string { return "access token that lasts " + limitSpan(l.OAuthAccessTTL) }),
	phrase("expires after 90 days without use", knob("OAUTH_REFRESH_TTL_DAYS"), func(l *config.Limits) string { return "expires after " + limitSpan(l.OAuthRefreshTTL) + " without use" }),
	phrase("refresh tokens expire after 90 days", knob("OAUTH_REFRESH_TTL_DAYS"), func(l *config.Limits) string { return "refresh tokens expire after " + limitSpan(l.OAuthRefreshTTL) }),

	// DOMAIN_UNPROVEN_HOURS
	phrase("expires 24 hours after binding", knob("DOMAIN_UNPROVEN_HOURS"), func(l *config.Limits) string { return "expires " + limitSpan(l.DomainUnprovenTTL) + " after binding" }),
	phrase("bound_at plus 24 hours", knob("DOMAIN_UNPROVEN_HOURS"), func(l *config.Limits) string { return "bound_at plus " + limitSpan(l.DomainUnprovenTTL) }),
	phrase("lapses after 24 hours", knob("DOMAIN_UNPROVEN_HOURS"), func(l *config.Limits) string { return "lapses after " + limitSpan(l.DomainUnprovenTTL) }),
	phrase("unproven bindings after 24 hours", knob("DOMAIN_UNPROVEN_HOURS"), func(l *config.Limits) string { return "unproven bindings after " + limitSpan(l.DomainUnprovenTTL) }),
	phrase("expires after 24 hours", knob("DOMAIN_UNPROVEN_HOURS"), func(l *config.Limits) string { return "expires after " + limitSpan(l.DomainUnprovenTTL) }),

	// DOMAIN_LAPSE_WARN_HOURS, DOMAIN_LAPSE_HOURS
	phrase("for 24 hours the owner is emailed, and after 72 hours", []string{"DOMAIN_LAPSE_WARN_HOURS", "DOMAIN_LAPSE_HOURS"}, func(l *config.Limits) string {
		return "for " + limitSpan(l.DomainLapseWarnAfter) + " the owner is emailed, and after " + limitSpan(l.DomainLapseAfter)
	}),
	phrase("the owner is emailed after 24 hours; after 72 hours", []string{"DOMAIN_LAPSE_WARN_HOURS", "DOMAIN_LAPSE_HOURS"}, func(l *config.Limits) string {
		return "the owner is emailed after " + limitSpan(l.DomainLapseWarnAfter) + "; after " + limitSpan(l.DomainLapseAfter)
	}),
	phrase("If it fails every check for a day", knob("DOMAIN_LAPSE_WARN_HOURS"), func(l *config.Limits) string {
		return "If it fails every check for " + limitSpan(l.DomainLapseWarnAfter)
	}),
	phrase("After three days the domain is disconnected", knob("DOMAIN_LAPSE_HOURS"), func(l *config.Limits) string {
		return "After " + limitSpan(l.DomainLapseAfter) + " the domain is disconnected"
	}),
	phrase("after three days, be disconnected", knob("DOMAIN_LAPSE_HOURS"), func(l *config.Limits) string { return "after " + limitSpan(l.DomainLapseAfter) + ", be disconnected" }),

	// DOMAIN_CHECK_INTERVAL_MINUTES
	phrase("about every two minutes", knob("DOMAIN_CHECK_INTERVAL_MINUTES"), func(l *config.Limits) string { return "about every " + limitSpan(l.DomainCheckInterval) }),
	phrase("background check every two minutes", knob("DOMAIN_CHECK_INTERVAL_MINUTES"), func(l *config.Limits) string { return "background check every " + limitSpan(l.DomainCheckInterval) }),
	phrase("every 2 minutes (releasing", knob("DOMAIN_CHECK_INTERVAL_MINUTES"), func(l *config.Limits) string { return "every " + limitSpan(l.DomainCheckInterval) + " (releasing" }),

	// DOMAIN_CERTS_PER_ACCOUNT_DAILY
	phrase("at most 5 new domain certificates per account per day", knob("DOMAIN_CERTS_PER_ACCOUNT_DAILY"), func(l *config.Limits) string {
		return "at most " + limitNum(l.DomainCertsDaily) + " new domain certificates per account per day"
	}),
	phrase("at most 5 new domain certificates a day", knob("DOMAIN_CERTS_PER_ACCOUNT_DAILY"), func(l *config.Limits) string {
		return "at most " + limitNum(l.DomainCertsDaily) + " new domain certificates a day"
	}),

	// EVENT_TTL_DAYS
	phrase("A claim expires after three weeks", knob("EVENT_TTL_DAYS"), func(l *config.Limits) string { return "A claim expires after " + limitSpan(l.EventTTL) }),
	phrase("A claim lasts three weeks", knob("EVENT_TTL_DAYS"), func(l *config.Limits) string { return "A claim lasts " + limitSpan(l.EventTTL) }),

	// DELETED_RETENTION_DAYS
	phrase("Recently deleted for 7 days", knob("DELETED_RETENTION_DAYS"), func(l *config.Limits) string { return "Recently deleted for " + limitSpan(l.DeletedRetention) }),
	phrase("After 7 days it is removed for good", knob("DELETED_RETENTION_DAYS"), func(l *config.Limits) string {
		return "After " + limitSpan(l.DeletedRetention) + " it is removed for good"
	}),
	phrase("deleted in the last 7 days", knob("DELETED_RETENTION_DAYS"), func(l *config.Limits) string { return "deleted in the last " + limitSpan(l.DeletedRetention) }),
	phrase("for 7 days with its name held", knob("DELETED_RETENTION_DAYS"), func(l *config.Limits) string { return "for " + limitSpan(l.DeletedRetention) + " with its name held" }),
	phrase("restorable for 7 days", knob("DELETED_RETENTION_DAYS"), func(l *config.Limits) string { return "restorable for " + limitSpan(l.DeletedRetention) }),
	phrase("kept for 7 days with their saved data", knob("DELETED_RETENTION_DAYS"), func(l *config.Limits) string {
		return "kept for " + limitSpan(l.DeletedRetention) + " with their saved data"
	}),
	phrase("For 7 days you can restore it", knob("DELETED_RETENTION_DAYS"), func(l *config.Limits) string { return "For " + limitSpan(l.DeletedRetention) + " you can restore it" }),
	phrase("Recently deleted within 7 days", knob("DELETED_RETENTION_DAYS"), func(l *config.Limits) string { return "Recently deleted within " + limitSpan(l.DeletedRetention) }),
	phrase("keeps it for 7 days", knob("DELETED_RETENTION_DAYS"), func(l *config.Limits) string { return "keeps it for " + limitSpan(l.DeletedRetention) }),
	phrase("After <b>7 days</b> it is gone", knob("DELETED_RETENTION_DAYS"), func(l *config.Limits) string { return "After <b>" + limitSpan(l.DeletedRetention) + "</b> it is gone" }),

	// IDLE_AFTER_DAYS, IDLE_GRACE_DAYS
	phrase("for 90 days gets its owner", knob("IDLE_AFTER_DAYS"), func(l *config.Limits) string { return "for " + limitSpan(l.IdleAfter) + " gets its owner" }),
	phrase("no new version for 90 days", knob("IDLE_AFTER_DAYS"), func(l *config.Limits) string { return "no new version for " + limitSpan(l.IdleAfter) }),
	phrase("under 90 days of visit records", knob("IDLE_AFTER_DAYS"), func(l *config.Limits) string { return "under " + limitSpan(l.IdleAfter) + " of visit records" }),
	phrase("updates for 90 days", knob("IDLE_AFTER_DAYS"), func(l *config.Limits) string { return "updates for " + limitSpan(l.IdleAfter) }),
	phrase("30 days later with nothing done", knob("IDLE_GRACE_DAYS"), func(l *config.Limits) string { return limitSpan(l.IdleGrace) + " later with nothing done" }),
	phrase("warned 30 days ago", knob("IDLE_GRACE_DAYS"), func(l *config.Limits) string { return "warned " + limitSpan(l.IdleGrace) + " ago" }),

	// ANALYTICS_RETENTION_DAYS
	phrase("Analytics records are deleted after 400 days", knob("ANALYTICS_RETENTION_DAYS"), func(l *config.Limits) string {
		return "Analytics records are deleted after " + limitDays(l.AnalyticsRetention)
	}),
	phrase("Analytics records:</strong> 400 days", knob("ANALYTICS_RETENTION_DAYS"), func(l *config.Limits) string { return "Analytics records:</strong> " + limitDays(l.AnalyticsRetention) }),
	phrase("prune at 400 days", knob("ANALYTICS_RETENTION_DAYS"), func(l *config.Limits) string { return "prune at " + limitDays(l.AnalyticsRetention) }),
	phrase("pruned at 400 days", knob("ANALYTICS_RETENTION_DAYS"), func(l *config.Limits) string { return "pruned at " + limitDays(l.AnalyticsRetention) }),

	// API_METRICS_RETENTION_DAYS
	phrase("caller IPs are kept 30 days", knob("API_METRICS_RETENTION_DAYS"), func(l *config.Limits) string { return "caller IPs are kept " + limitDays(l.APIMetricsRetention) }),
	phrase("exact address) for 30 days", knob("API_METRICS_RETENTION_DAYS"), func(l *config.Limits) string { return "exact address) for " + limitDays(l.APIMetricsRetention) }),
	phrase("shortened IP for 30 days", knob("API_METRICS_RETENTION_DAYS"), func(l *config.Limits) string { return "shortened IP for " + limitDays(l.APIMetricsRetention) }),
	phrase("from API calls:</strong> 30 days", knob("API_METRICS_RETENTION_DAYS"), func(l *config.Limits) string { return "from API calls:</strong> " + limitDays(l.APIMetricsRetention) }),
	phrase("prunes at 30 days", knob("API_METRICS_RETENTION_DAYS"), func(l *config.Limits) string { return "prunes at " + limitDays(l.APIMetricsRetention) }),
	phrase("30-day retention", knob("API_METRICS_RETENTION_DAYS"), func(l *config.Limits) string {
		return limitSpanAdj(time.Duration(l.APIMetricsRetention)*24*time.Hour) + " retention"
	}),

	// AI_JOB_TIMEOUT_MINUTES, AI_MAX_JOBS_PER_USER, AI_MAX_JOBS
	phrase("8-minute run ceiling", knob("AI_JOB_TIMEOUT_MINUTES"), func(l *config.Limits) string { return limitSpanAdj(l.AIJobTimeout) + " run ceiling" }),
	phrase("3 builds in flight per user, 64 in total", []string{"AI_MAX_JOBS_PER_USER", "AI_MAX_JOBS"}, func(l *config.Limits) string {
		return limitNum(l.AIMaxJobsPerUser) + " builds in flight per user, " + limitNum(l.AIMaxJobs) + " in total"
	}),
	phrase("Three builds at once", knob("AI_MAX_JOBS_PER_USER"), func(l *config.Limits) string {
		if l.AIMaxJobsPerUser == 1 {
			return "One build at a time"
		}
		return limitNum(l.AIMaxJobsPerUser) + " builds at once"
	}),

	// RATE_LIMIT_AI_USER, RATE_LIMIT_AI_IP
	phrase("per user (burst 30, +1 per 10 s)", knob("RATE_LIMIT_AI_USER"), func(l *config.Limits) string {
		return "per user (burst " + limitNum(l.RateAIUser.Burst) + ", " + perEvery(l.RateAIUser) + ")"
	}),
	phrase("per IP (burst 20, +1 per 12 s)", knob("RATE_LIMIT_AI_IP"), func(l *config.Limits) string {
		return "per IP (burst " + limitNum(l.RateAIIP.Burst) + ", " + perEvery(l.RateAIIP) + ")"
	}),

	// SAVED_DATA_UNDO_DAYS
	phrase("30 days (SAVED_DATA_UNDO_DAYS", knob("SAVED_DATA_UNDO_DAYS"), func(l *config.Limits) string { return undoDays(l) + " (SAVED_DATA_UNDO_DAYS" }),
	phrase("30 days, most recently deleted", knob("SAVED_DATA_UNDO_DAYS"), func(l *config.Limits) string { return undoDays(l) + ", most recently deleted" }),
	phrase("from before it for 30 days (GET", knob("SAVED_DATA_UNDO_DAYS"), func(l *config.Limits) string { return "from before it for " + undoDays(l) + " (GET" }),
	phrase("before is kept for 30 days (GET", knob("SAVED_DATA_UNDO_DAYS"), func(l *config.Limits) string { return "before is kept for " + undoDays(l) + " (GET" }),
	phrase("deleted in the last 30 days, is listed", knob("SAVED_DATA_UNDO_DAYS"), func(l *config.Limits) string { return "deleted in the last " + undoDays(l) + ", is listed" }),
	phrase("kept 30 days. Logged.", knob("SAVED_DATA_UNDO_DAYS"), func(l *config.Limits) string { return "kept " + undoDays(l) + ". Logged." }),
	phrase("the last 30 days, newest first", knob("SAVED_DATA_UNDO_DAYS"), func(l *config.Limits) string { return "the last " + undoDays(l) + ", newest first" }),
	phrase("saved-data document in the last 30 days", knob("SAVED_DATA_UNDO_DAYS"), func(l *config.Limits) string { return "saved-data document in the last " + undoDays(l) }),
	phrase("Recently deleted for 30 days, where the owner", knob("SAVED_DATA_UNDO_DAYS"), func(l *config.Limits) string {
		return "Recently deleted for " + undoDays(l) + ", where the owner"
	}),
	phrase("Recently deleted for 30 days). In a public list", knob("SAVED_DATA_UNDO_DAYS"), func(l *config.Limits) string {
		return "Recently deleted for " + undoDays(l) + "). In a public list"
	}),
	phrase("list item is kept for 30 days with who", knob("SAVED_DATA_UNDO_DAYS"), func(l *config.Limits) string { return "list item is kept for " + undoDays(l) + " with who" }),
	phrase("Saved data has a 30-day undo.", knob("SAVED_DATA_UNDO_DAYS"), func(l *config.Limits) string {
		return "Saved data has a " + limitSpanAdj(time.Duration(l.SavedData.UndoDays)*24*time.Hour) + " undo."
	}),
	phrase("Every change is kept for 30 days: the owner", knob("SAVED_DATA_UNDO_DAYS"), func(l *config.Limits) string { return "Every change is kept for " + undoDays(l) + ": the owner" }),
	phrase("Recently deleted for <b>30 days</b>, then", knob("SAVED_DATA_UNDO_DAYS"), func(l *config.Limits) string { return "Recently deleted for <b>" + undoDays(l) + "</b>, then" }),
	phrase("deleted** for 30 days: `GET", knob("SAVED_DATA_UNDO_DAYS"), func(l *config.Limits) string { return "deleted** for " + undoDays(l) + ": `GET" }),
	phrase("the earlier copy is kept for 30 days so the owner", knob("SAVED_DATA_UNDO_DAYS"), func(l *config.Limits) string {
		return "the earlier copy is kept for " + undoDays(l) + " so the owner"
	}),
	phrase("Recently deleted; after 30 days it is gone for good", knob("SAVED_DATA_UNDO_DAYS"), func(l *config.Limits) string {
		return "Recently deleted; after " + undoDays(l) + " it is gone for good"
	}),

	// SAVED_DATA_SITE_MAX_MB, SAVED_DATA_HISTORY_MAX_MB, SAVED_DATA_SNAPSHOT_EVERY
	phrase("full (50 MB of page data", knob("SAVED_DATA_SITE_MAX_MB"), func(l *config.Limits) string { return "full (" + limitNum(l.SavedData.SiteMaxMB) + " MB of page data" }),
	phrase("is capped at 50 MB.", knob("SAVED_DATA_SITE_MAX_MB"), func(l *config.Limits) string { return "is capped at " + limitNum(l.SavedData.SiteMaxMB) + " MB." }),
	phrase("past 50 MB (SAVED_DATA_SITE_MAX_MB)", knob("SAVED_DATA_SITE_MAX_MB"), func(l *config.Limits) string {
		return "past " + limitNum(l.SavedData.SiteMaxMB) + " MB (SAVED_DATA_SITE_MAX_MB)"
	}),
	phrase("list items) past 50 MB. The owner", knob("SAVED_DATA_SITE_MAX_MB"), func(l *config.Limits) string {
		return "list items) past " + limitNum(l.SavedData.SiteMaxMB) + " MB. The owner"
	}),
	phrase("list items, past 50 MB; history", knob("SAVED_DATA_SITE_MAX_MB"), func(l *config.Limits) string {
		return "list items, past " + limitNum(l.SavedData.SiteMaxMB) + " MB; history"
	}),
	phrase("(SAVED_DATA_HISTORY_MAX_MB, 20 MB)", knob("SAVED_DATA_HISTORY_MAX_MB"), func(l *config.Limits) string {
		return "(SAVED_DATA_HISTORY_MAX_MB, " + limitNum(l.SavedData.HistoryMaxMB) + " MB)"
	}),
	phrase("every 50 changes (SAVED_DATA_SNAPSHOT_EVERY", knob("SAVED_DATA_SNAPSHOT_EVERY"), func(l *config.Limits) string {
		return "every " + limitNum(l.SavedData.SnapshotEvery) + " changes (SAVED_DATA_SNAPSHOT_EVERY"
	}),

	// SAVED_DATA_READ_PER_SEC, SAVED_DATA_READ_BURST, SAVED_DATA_APPEND_PER_MIN, SAVED_DATA_IDEMPOTENCY_HOURS
	phrase("reads allow 30 a second per address with a burst of 60", []string{"SAVED_DATA_READ_PER_SEC", "SAVED_DATA_READ_BURST"}, func(l *config.Limits) string {
		return "reads allow " + limitNum(l.SavedData.ReadPerSec) + " a second per address with a burst of " + limitNum(l.SavedData.ReadBurst)
	}),
	phrase("reads: 30 a second per site", knob("SAVED_DATA_READ_PER_SEC"), func(l *config.Limits) string {
		return "reads: " + limitNum(l.SavedData.ReadPerSec) + " a second per site"
	}),
	phrase("limited to 30 a second per visitor", knob("SAVED_DATA_READ_PER_SEC"), func(l *config.Limits) string {
		return "limited to " + limitNum(l.SavedData.ReadPerSec) + " a second per visitor"
	}),
	phrase("limited to 30 a minute per address", knob("SAVED_DATA_APPEND_PER_MIN"), func(l *config.Limits) string {
		return "limited to " + limitNum(l.SavedData.AppendPerMin) + " a minute per address"
	}),
	phrase("owner's key: 30 a minute per address", knob("SAVED_DATA_APPEND_PER_MIN"), func(l *config.Limits) string {
		return "owner's key: " + limitNum(l.SavedData.AppendPerMin) + " a minute per address"
	}),
	phrase("for 24 hours (SAVED_DATA_IDEMPOTENCY_HOURS", knob("SAVED_DATA_IDEMPOTENCY_HOURS"), func(l *config.Limits) string {
		return "for " + limitSpan(time.Duration(l.SavedData.IdempotencyHours)*time.Hour) + " (SAVED_DATA_IDEMPOTENCY_HOURS"
	}),
}

// undoDays words SAVED_DATA_UNDO_DAYS: "30 days", "one day".
func undoDays(l *config.Limits) string { return limitDays(l.SavedData.UndoDays) }

// limitsRewriter substitutes the configured limits into served text. Nil when
// every phrase already says what the settings say.
type limitsRewriter struct{ r *strings.Replacer }

func newLimitsRewriter(l config.Limits) *limitsRewriter {
	def := config.DefaultLimits()
	var pairs []string
	for _, ph := range limitPhrases {
		if now := ph.say(&l); now != ph.say(&def) {
			pairs = append(pairs, ph.text, now)
		}
	}
	if len(pairs) == 0 {
		return nil
	}
	return &limitsRewriter{r: strings.NewReplacer(pairs...)}
}

func (h *limitsRewriter) apply(b []byte) []byte {
	if h == nil {
		return b
	}
	return []byte(h.r.Replace(string(b)))
}

// instanceLimits is this install's limits rewriter, set by ApplyLimits before
// serving. Nil when every knob is at its default.
var instanceLimits *limitsRewriter

// assetsRewritten reports whether the text assets (llms.txt, the spec, ...)
// need a rewriting handler rather than the plain file server.
func assetsRewritten() bool { return instanceHosts != nil || instanceLimits != nil }

// rewriteServedText applies both serve-time rewrites (hostnames, then limits)
// to a text asset.
func rewriteServedText(b []byte) []byte {
	return instanceLimits.apply(instanceHosts.apply(b))
}
