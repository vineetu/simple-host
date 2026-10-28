package mcp

import "strings"

func hackPublicReadOnly() map[string]any {
	annotations := readOnly()
	annotations["openWorldHint"] = true
	return annotations
}

// Filter before serializing either representation. REST responses remain intact
// for browser pages, and these optional fields need no output schema changes.
func minimiseHackMemberResult(name, userID string, out map[string]any) {
	switch name {
	case "hack_join_event", "hack_join_judge", "hack_application_status", "hack_get_team_status":
		if me, ok := out["me"].(map[string]any); ok {
			delete(me, "team_key")
		}
	case "hack_get_my_conflicts":
		if rows, ok := out["conflicts"].([]any); ok {
			own := make([]any, 0, len(rows))
			for _, row := range rows {
				conflict, ok := row.(map[string]any)
				if ok && hackOwnConflict(conflict, userID) {
					delete(conflict, "judge_id")
					own = append(own, conflict)
				}
			}
			out["conflicts"] = own
		}
	case "hack_declare_my_conflict":
		if hackOwnConflict(out, userID) {
			delete(out, "judge_id")
		} else {
			clear(out)
		}
	}
}

// OAuth callers carry UserID; personal API-key callers carry Owner.
func (c Caller) accountUserID() string {
	if c.UserID != "" {
		return c.UserID
	}
	return c.Owner
}

func hackOwnConflict(conflict map[string]any, userID string) bool {
	judgeID, _ := conflict["judge_id"].(string)
	return userID != "" && strings.EqualFold(judgeID, userID)
}

func minimiseHackOrganiserResult(name string, includeEmails bool, out map[string]any) {
	switch name {
	case "hack_decide_application":
		delete(out, "user_id")
	case "hack_get_teams":
		if !includeEmails {
			teams, _ := out["teams"].([]any)
			for _, row := range teams {
				if team, ok := row.(map[string]any); ok {
					hackOmitRowField(team["members"], "email")
				}
			}
			hackOmitRowField(out["on_no_team"], "email")
		}
	case "hack_move_member", "hack_set_team_deadline", "hack_take_down_team_site", "hack_restore_team_site":
		for field := range out {
			switch field {
			case "name", "slug":
				continue
			case "site", "deadline", "deadline_override", "frozen", "extended", "pinned_version", "pinned_url":
				if name != "hack_move_member" {
					continue
				}
			}
			delete(out, field)
		}
	case "hack_get_team_key":
		if key, ok := out["key"].(map[string]any); ok {
			delete(key, "last4")
			delete(key, "created_at")
			delete(key, "last_used_at")
		}
	case "hack_get_judging_dashboard":
		hackOmitRowField(out["judges"], "judge_id")
	}
}

func hackOmitRowField(value any, field string) {
	rows, _ := value.([]any)
	for _, row := range rows {
		if obj, ok := row.(map[string]any); ok {
			delete(obj, field)
		}
	}
}
