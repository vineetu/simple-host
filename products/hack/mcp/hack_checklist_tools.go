package hackmcp

import (
	"encoding/json"
	"fmt"
	"github.com/vsriram/simple-host/internal/mcp"
	"net/http"
	"net/url"
)

func hackChecklistTools() []mcp.Tool {
	slug := map[string]any{"slug": mcp.Str("Event slug.")}
	tools := []mcp.Tool{
		{Name: "hack_get_checklist", Title: "Read event checklist", Description: "Read the membership role's checklist and next action. Organisers may also request judge. Records due entry deadline pins.", InputSchema: mcp.Object(checklistWith(slug, map[string]any{"role": map[string]any{"type": "string", "enum": []string{"organiser", "judge", "participant"}}}), "slug"), Annotations: mcp.Writes(false, true, false)},
		{Name: "hack_set_checklist_not_needed", Title: "Mark an optional item not needed", Description: "Set or restore a shared optional organiser checklist item. Ask first. Ended events refuse changes.", InputSchema: mcp.Object(checklistWith(slug, map[string]any{"item_id": mcp.Str("Optional organiser item ID."), "not_needed": map[string]any{"type": "boolean"}}), "slug", "item_id", "not_needed"), Annotations: mcp.Writes(false, true, false)},
		{Name: "hack_acknowledge_checklist_item", Title: "Acknowledge a checklist item", Description: "Explicitly confirm your j.rubric or j.conflicts review. Ask first. Rubric items need the current checklist version; conflicts are unversioned.", InputSchema: mcp.Object(checklistWith(slug, map[string]any{"item_id": map[string]any{"type": "string", "enum": []string{"j.rubric", "j.conflicts"}, "description": "j.rubric or j.conflicts."}, "version": mcp.Str("Current rubric item's version; omit for conflicts.")}), "slug", "item_id"), Annotations: mcp.Writes(false, true, false)},
	}
	for i := range tools {
		name := tools[i].Name
		tools[i].OutputSchema = mcp.OutObject(map[string]any{"can": hackAbilitiesSchema(), "checklist": hackChecklistSchema()}, "can", "checklist")
		tools[i] = mcp.WithRun(mcp.WithPolicy(tools[i], mcp.RecordsPins()), func(c *mcp.Call, args map[string]any) (mcp.Output, error) {
			slug, err := eventSlug(args)
			if err != nil {
				return mcp.Output{}, err
			}
			path := "/v1/hack/events/" + url.PathEscape(slug) + "/checklist"
			method := http.MethodGet
			var body []byte
			if name == "hack_get_checklist" {
				if role, ok, e := presentString(args, "role"); e != nil {
					return mcp.Output{}, e
				} else if ok {
					path += "?role=" + url.QueryEscape(role)
				}
			} else {
				item, err := mcp.StringArg(args, "item_id")
				if err != nil {
					return mcp.Output{}, err
				}
				if name == "hack_set_checklist_not_needed" {
					marked, ok := args["not_needed"].(bool)
					if !ok {
						return mcp.Output{}, fmt.Errorf("not_needed must be a boolean")
					}
					path += "/not-needed/" + url.PathEscape(item)
					method = http.MethodDelete
					if marked {
						method = http.MethodPost
					}
				} else {
					path += "/acknowledgements/" + url.PathEscape(item)
					method = http.MethodPost
					payload := map[string]any{}
					if version, ok, e := presentString(args, "version"); e != nil {
						return mcp.Output{}, e
					} else if ok {
						payload["version"] = version
					}
					body, _ = json.Marshal(payload)
				}
			}
			res := c.Do(method, path, body, nil)
			if !res.OK() {
				return mcp.Output{}, hackRestError(name, res)
			}
			var out map[string]any
			if err = json.Unmarshal(res.Body(), &out); err != nil {
				return mcp.Output{}, err
			}
			return mcp.Output{Text: mcp.JSONText(out), Structured: out}, nil
		})
	}
	return tools
}

func hackChecklistSchema() map[string]any {
	link := checklistNullableObject(mcp.OutObject(map[string]any{"screen": mcp.OutString("Screen."), "section": mcp.OutString("Section."), "tab": checklistNullableString("Tab."), "href": mcp.OutString("Resolved destination.")}, "screen", "section", "tab", "href"))
	item := mcp.OutObject(map[string]any{
		"id": mcp.OutString("Stable item ID."), "label": mcp.OutString("Task."), "state": mcp.OutString("done, not_needed, optional or todo."), "optional": mcp.OutBool("Optional recommendation."), "can_dismiss": mcp.OutBool("May mark not needed."), "origin_stage": mcp.OutString("First stage."), "group": mcp.OutString("current, earlier_required, earlier_optional or history."), "actionable": mcp.OutBool("Action is available."), "target_id": mcp.OutString("Navigation target."), "link": link, "detail": mcp.OutString("Facts and counts."), "count": map[string]any{"type": []string{"integer", "null"}, "description": "Completed count."}, "total": map[string]any{"type": []string{"integer", "null"}, "description": "Total count."}, "settled": mcp.OutBool("Incomplete settled entry."), "blocked_reason": checklistNullableString("Why action is unavailable."), "version": mcp.OutString("Opaque current rubric version."),
	}, "id", "label", "state", "optional", "can_dismiss", "origin_stage", "group", "actionable", "target_id", "link", "detail", "count", "total", "settled", "blocked_reason")
	next := checklistNullableObject(mcp.OutObject(map[string]any{"kind": mcp.OutString("Action kind."), "item_id": mcp.OutString("Leading item."), "target_id": mcp.OutString("Target."), "label": mcp.OutString("Action label."), "link": link, "stage": mcp.OutString("Stage to move to."), "confirmation_required": mcp.OutBool("Ask for confirmation before acting."), "blocked_reason": checklistNullableString("Existing forward-action blocker."), "cue": map[string]any{"type": []string{"object", "null"}, "additionalProperties": true}, "deadline_passed": mcp.OutBool("Entry deadline passed.")}, "kind", "label", "link", "confirmation_required", "blocked_reason"))
	return mcp.OutObject(map[string]any{"role": mcp.OutString("Checklist role."), "stage": mcp.OutString("Event stage."), "map_version": mcp.OutInteger("Navigation map version."), "items": mcp.OutArray("Selected checklist rows.", item), "next": next}, "role", "stage", "map_version", "items", "next")
}

func checklistWith(a, b map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}
func checklistNullableObject(s map[string]any) map[string]any {
	out := checklistWith(s, nil)
	out["type"] = []string{"object", "null"}
	return out
}
func checklistNullableString(description string) map[string]any {
	return map[string]any{"type": []string{"string", "null"}, "description": description}
}
