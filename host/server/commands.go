package server

import (
	"database/sql"
	"fmt"
	"os"

	"github.com/vsriram/simple-host/host/extension"
)

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
		{"geoip-verify", geoipVerify},
		{"migrate", func(args []string) int { return runMigrateCommand(args, exts) }},
		{"site-summary-backfill", runSiteSummaryBackfill},
		{"api-growth-backfill", runAPIGrowthBackfill},
		{"settings", func(args []string) int { return runSettingsCommand(args, exts) }},
		{"version", versionCommand},
		{"--version", versionCommand},
	}
}

// openCommandDB opens DB_DSN for a command; on failure it has said why and
// returns nil and the command's exit code.
func openCommandDB() (*sql.DB, int) {
	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		fmt.Fprintln(os.Stderr, "DB_DSN is not set")
		return nil, 2
	}
	database, err := sql.Open("postgres", dsn)
	if err != nil {
		fmt.Fprintln(os.Stderr, "open postgres:", err)
		return nil, 1
	}
	return database, 0
}
