package storage

import (
	"context"

	ruleengine "github.com/vsriram/simple-host/host/storage/rules"
	"github.com/vsriram/simple-host/internal/model"
)

func (s *Service) compileBindingRules(c *Call, table string) error {
	if c.Resource.RulesStatus() != "rules" {
		return rulesInvalid()
	}
	set, errs := ruleengine.DecodeSet(effectiveRules(*c, table))
	if len(errs) != 0 {
		return rulesInvalid()
	}
	return s.compileCallRules(c, ruleBindingTable(set, table))
}

// RuleRights describes possible operations, not permission to access a particular
// row. The visitor tools execute data requests through the ordinary row routes.
func (s *Service) RuleRights(ctx context.Context, c Call, table string) (map[string]any, error) {
	c.Context = ctx
	if err := s.Gate(c, false); err != nil {
		return nil, err
	}
	if err := s.compileBindingRules(&c, table); err != nil {
		return nil, err
	}
	rights := map[string]any{}
	for _, action := range []struct {
		name string
		op   ruleengine.Op
	}{
		{"read", ruleengine.OpRead}, {"add", ruleengine.OpAdd},
		{"edit_own", ruleengine.OpEdit}, {"delete_own", ruleengine.OpDelete},
	} {
		if c.VisitorID != "" && c.rulePlan.NeedsIdentity(action.op) {
			if err := s.visitorActive(&c); err != nil {
				return nil, err
			}
		}
		if err := s.ruleValues(ctx, &c, action.op, true); err != nil {
			return nil, err
		}
		rights[action.name] = c.rulePlan.Partial(action.op, c.ruleValues) != ruleengine.False
	}
	rights["read_own_only"] = c.rulePlan.ReadIsOwnOrOwner() && !c.OwnerOnSite
	return rights, nil
}

// HasChecked reports whether any add check on a resource needs the spam check.
// Files and unconverted resources keep their preset behaviour.
func (s *Service) HasChecked(c Call) bool {
	if c.Resource.RulesStatus() == "preset" {
		return c.Resource.HasChecked()
	}
	checked := func(table string) bool {
		copy := c
		return s.compileBindingRules(&copy, table) == nil && copy.rulePlan.ReferencesBinding(ruleengine.OpAdd, "auth.checked")
	}
	if checked("") {
		return true
	}
	found := false
	if c.Resource.TableRange != nil {
		c.Resource.TableRange(func(name string, _ model.TableRule) { found = found || checked(name) })
	} else {
		for name := range c.Resource.Tables {
			found = found || checked(name)
		}
	}
	return found
}
