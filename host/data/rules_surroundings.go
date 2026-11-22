package data

import (
	"context"

	"github.com/vsriram/simple-host/host/model"
	ruleengine "github.com/vsriram/simple-host/host/storage/rules"
)

func (s *Service) CompileBindingRules(c *Call, table string) error {
	if c.Resource.RulesStatus() != "rules" {
		return RulesInvalid()
	}
	set, errs := ruleengine.DecodeSet(c.Resource.EffectiveRules(table))
	if len(errs) != 0 {
		return RulesInvalid()
	}
	return s.CompileCallRules(c, RuleBindingTable(set, table))
}

// RuleRights describes possible operations, not permission to access a particular
// row. The visitor tools execute data requests through the ordinary row routes.
func (s *Service) RuleRights(ctx context.Context, c Call, table string) (map[string]any, error) {
	c.Context = ctx
	if err := s.Gate(c, false); err != nil {
		return nil, err
	}
	if err := s.CompileBindingRules(&c, table); err != nil {
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
		if c.VisitorID != "" && c.RulePlan.NeedsIdentity(action.op) {
			if err := s.VisitorActive(&c); err != nil {
				return nil, err
			}
		}
		if err := s.RuleValues(ctx, &c, action.op, true); err != nil {
			return nil, err
		}
		rights[action.name] = c.RulePlan.Partial(action.op, c.RuleValues) != ruleengine.False
	}
	rights["read_own_only"] = c.RulePlan.ReadIsOwnOrOwner() && !c.OwnerOnSite
	return rights, nil
}

// HasChecked reports whether any add check on a resource needs the spam check.
// Files never take the spam check.
func (s *Service) HasChecked(c Call) bool {
	checked := func(table string) bool {
		copy := c
		return s.CompileBindingRules(&copy, table) == nil && copy.RulePlan.ReferencesBinding(ruleengine.OpAdd, "auth.checked")
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
