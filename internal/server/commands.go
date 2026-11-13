package server

import "github.com/vsriram/simple-host/internal/extension"

type command struct {
	name string
	run  func([]string) int
}

func commands(exts []*extension.Extension) []command {
	set := migrationSet(exts)
	versionCommand := func([]string) int { return runVersionCommand(set) }
	return []command{
		{"prune-versions", runPruneVersions},
		{"oauth-client", runOAuthClientCommand},
		{"review-account", runReviewAccountCommand},
		{"move-site-base", runMoveSiteBaseCommand},
		{"brain-build", runBrainBuild},
		{"geoip-verify", geoipVerify},
		{"migrate", func(args []string) int { return runMigrateCommand(args, set) }},
		{"site-summary-backfill", runSiteSummaryBackfill},
		{"api-growth-backfill", runAPIGrowthBackfill},
		{"settings", runSettingsCommand},
		{"version", versionCommand},
		{"--version", versionCommand},
	}
}
