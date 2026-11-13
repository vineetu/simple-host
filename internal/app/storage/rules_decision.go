package storage

import ruleengine "github.com/vsriram/simple-host/host/storage/rules"

// rulePermission is the binding-level decision shared by the conversion oracle
// and rules enforcement. Gates and the active-account check precede this helper;
// a permitted result still requires the statement's row predicates.
func rulePermission(p *ruleengine.Plan, set ruleengine.PolicySet, op ruleengine.Op, v ruleengine.Values, spamCode string) (bool, string) {
	partial := p.Partial(op, v)
	// Own-row comparisons remain Unknown even with a NULL auth.id. Apply the
	// anonymous identity refusal before letting such a request reach the row.
	if !v["auth.signed_in"].Bool && partial != ruleengine.True && p.NeedsIdentity(op) {
		signedIn := cloneRuleValues(v)
		signedIn["auth.signed_in"] = ruleengine.Bool(true)
		signedIn["auth.role"] = ruleengine.Text("visitor")
		for _, name := range []string{"auth.id", "auth.team", "auth.teams"} {
			delete(signedIn, name)
		}
		if p.Partial(op, signedIn) != ruleengine.False {
			return false, "sign_in_required"
		}
	}
	if partial != ruleengine.False {
		return true, ""
	}
	checked := cloneRuleValues(v)
	checked["auth.checked"] = ruleengine.Bool(true)
	if op == ruleengine.OpAdd && p.Partial(op, checked) != ruleengine.False {
		if spamCode == "" {
			spamCode = "spam_check_required"
		}
		return false, spamCode
	}
	if op == ruleengine.OpEdit || op == ruleengine.OpDelete {
		command := ruleengine.CmdEdit
		if op == ruleengine.OpDelete {
			command = ruleengine.CmdDelete
		}
		for _, policy := range set.Policies {
			if !policy.Restrictive && (policy.Command == command || policy.Command == ruleengine.CmdAll) && p.Partial(ruleengine.OpAdd, checked) != ruleengine.False {
				return false, "add_only"
			}
		}
	}
	return false, "forbidden"
}

func cloneRuleValues(v ruleengine.Values) ruleengine.Values {
	copy := make(ruleengine.Values, len(v))
	for name, value := range v {
		copy[name] = value
	}
	return copy
}

// ruleAIConstraint supplies the mandatory author term for visitor-AI writes.
// The same predicate feeds the oracle and R3-S1's statement builders.
func ruleAIConstraint(op ruleengine.Op, c Call) ruleengine.Predicate {
	if c.VisitorAI && (op == ruleengine.OpEdit || op == ruleengine.OpDelete) {
		return ruleengine.Predicate{SQL: `"visitor_id" = ?`, Args: []ruleengine.Value{ruleengine.Text(c.VisitorID)}}
	}
	return ruleengine.Predicate{SQL: "1"}
}
