package data

import (
	"context"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
)

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

// ResourceListJSON answers GET .../storage/resources: every resource of the
// site as the owner's tools see it.
func (s *Service) ResourceListJSON(ctx context.Context, call Call) ([]map[string]any, error) {
	list, err := s.ListStorageResources(ctx, call)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(list))
	for _, item := range list {
		out = append(out, ResourceRulesJSON(item.Resource, item.RulesErrors))
	}
	return out, nil
}

// SaveResource answers PUT .../storage/resources/{name}: body is the
// request's configuration (DecodeResourceInput), saved by
// PutStorageResource. The answer is the resource as the owner's tools see
// it, with any warnings; created reports a new resource.
func (s *Service) SaveResource(ctx context.Context, call Call, name string, body []byte) (map[string]any, bool, error) {
	in, err := DecodeResourceInput(body)
	if err != nil {
		return nil, false, err
	}
	in.Call, in.Name = call, name
	result, err := s.PutStorageResource(ctx, in)
	if err != nil {
		return nil, false, err
	}
	out := ResourceRulesJSON(result.Resource, result.RulesErrors)
	if len(result.Warnings) > 0 {
		out["warnings"] = result.Warnings
	}
	return out, result.Created, nil
}
