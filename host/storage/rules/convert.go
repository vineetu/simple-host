package rules

import (
	"fmt"
	"sort"
)

// Convert translates preset matrices without consulting a product or its data.
// A manual decision anywhere leaves the entire resource unconverted.
func Convert(in ConvertInput) ConvertOutput {
	out := ConvertOutput{Tables: make(map[string]PolicySet, len(in.Tables))}
	convert := func(table string, m Matrix) PolicySet {
		set := PolicySet{Version: 1, Policies: []Policy{}}
		for _, action := range []struct {
			command Command
			who     string
		}{{CmdRead, m.Read}, {CmdAdd, m.Add}, {CmdEdit, m.Edit}, {CmdDelete, m.Delete}} {
			var expr string
			switch action.who {
			case "nobody":
				continue
			case "owner":
				expr = `auth.role == "owner"`
			case "own":
				if action.command != CmdAdd {
					expr = `visitor_id == auth.id || auth.role == "owner"`
				}
			case "signed-in":
				expr = `auth.signed_in`
			case "checked":
				if action.command == CmdAdd {
					expr = `auth.checked || auth.ai || auth.role == "owner"`
				}
			case "anyone":
				if action.command == CmdRead || action.command == CmdAdd {
					expr = `true`
				}
			}
			if expr == "" {
				out.Manual = append(out.Manual, fmt.Sprintf("%s: %s %q needs a manual decision", table, action.command, action.who))
				continue
			}
			policy := Policy{Name: string(action.command), Command: action.command}
			if action.command == CmdAdd {
				policy.Check = expr
			} else {
				policy.Using = expr
			}
			set.Policies = append(set.Policies, policy)
		}
		if in.ApproveFirst && m.Add == "checked" {
			set.Policies = append(set.Policies, Policy{Name: "approve_first", Command: CmdRead,
				Using: `auth.role == "owner" || approved_at is not null`, Restrictive: true})
		}
		return set
	}
	out.Default = convert("default", in.Default)
	names := make([]string, 0, len(in.Tables))
	for name := range in.Tables {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		out.Tables[name] = convert(name, in.Tables[name])
	}
	if len(out.Manual) != 0 {
		return ConvertOutput{Manual: out.Manual}
	}
	return out
}
