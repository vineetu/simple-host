package server

type command struct {
	name string
	run  func([]string) int
}

var commands = []command{
	{"prune-versions", runPruneVersions},
	{"oauth-client", runOAuthClientCommand},
	{"review-account", runReviewAccountCommand},
	{"move-site-base", runMoveSiteBaseCommand},
	{"brain-build", runBrainBuild},
	{"geoip-verify", geoipVerify},
	{"migrate", runMigrateCommand},
	{"site-summary-backfill", runSiteSummaryBackfill},
	{"api-growth-backfill", runAPIGrowthBackfill},
	{"settings", runSettingsCommand},
	{"version", versionCommand},
	{"--version", versionCommand},
}

func versionCommand([]string) int { return runVersionCommand() }
