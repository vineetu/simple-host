package server

import "github.com/vsriram/simple-host/host/extension"

type command struct {
	name string
	run  func([]string) int
}

func commands(exts []*extension.Extension) []command {
	set := migrationSet(exts)
	versionCommand := func([]string) int { return runVersionCommand(set) }
	return []command{
		{"static", func(args []string) int { return runStaticCommand(args, exts) }},
		{"prune-versions", runPruneVersions},
		{"oauth-client", runOAuthClientCommand},
		{"review-account", runReviewAccountCommand},
		{"move-site-base", runMoveSiteBaseCommand},
		{"geoip-verify", geoipVerify},
		{"migrate", func(args []string) int { return runMigrateCommand(args, exts) }},
		{"site-summary-backfill", runSiteSummaryBackfill},
		{"api-growth-backfill", runAPIGrowthBackfill},
		{"settings", func(args []string) int { return runSettingsCommand(args, exts) }},
		{"version", versionCommand},
		{"--version", versionCommand},
	}
}
