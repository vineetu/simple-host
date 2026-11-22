package data

import (
	"context"

	"github.com/vsriram/simple-host/host/httpx"

	"github.com/vsriram/simple-host/host/data/repo"
	model "github.com/vsriram/simple-host/host/model"
)

// LoadResource reads a resource and its SQLite table access rules.
func (s *Service) LoadResource(ctx context.Context, siteID, name string) (model.Resource, error) {
	return repo.LoadResource(ctx, s.DB, siteID, name)
}

// ListResourceRecords reads resource records in name order with their table rules.
func (s *Service) ListResourceRecords(ctx context.Context, siteID string) ([]model.Resource, error) {
	return repo.ListResources(ctx, s.DB, siteID)
}

// ResourceRulesJSON is a resource as the owner's tools see it: the
// rules that govern it (the owner-only set when none are stored), their
// status ("invalid" when they no longer compile) and, for SQLite, its
// per-table rules.
func ResourceRulesJSON(x model.Resource, diagnostics []httpx.RuleError) map[string]any {
	if diagnostics == nil {
		diagnostics = []httpx.RuleError{}
	}
	status := x.RulesStatus()
	if len(diagnostics) > 0 {
		status = "invalid" // stored rules that no longer compile refuse every visitor
	}
	out := map[string]any{"name": x.Name, "kind": x.Kind, "site_passcode": x.SitePasscode, "rules": x.EffectiveRules(""), "rules_status": status, "rules_errors": diagnostics}
	if x.Kind == "sqlite" {
		tables := map[string]any{}
		if x.Rules != nil {
			for name, table := range x.Tables {
				tables[name] = map[string]any{"rules": table.Rules}
			}
		}
		out["tables"] = tables
	}
	return out
}
