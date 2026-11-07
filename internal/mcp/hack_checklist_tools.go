package mcp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

func hackChecklistTools() []Tool {
	slug := map[string]any{"slug": str("Event slug.")}
	tools := []Tool{
		{Name: "hack_get_checklist", Title: "Read event checklist", Description: "Read the membership role's checklist and next action. Organisers may also request judge. Records due entry deadline pins.", InputSchema: object(checklistWith(slug, map[string]any{"role": map[string]any{"type": "string", "enum": []string{"organiser", "judge", "participant"}}}), "slug"), Annotations: writes(false, true, false)},
		{Name: "hack_set_checklist_not_needed", Title: "Mark an optional item not needed", Description: "Set or restore a shared optional organiser checklist item. Ask first. Ended events refuse changes.", InputSchema: object(checklistWith(slug, map[string]any{"item_id": str("Optional organiser item ID."), "not_needed": map[string]any{"type": "boolean"}}), "slug", "item_id", "not_needed"), Annotations: writes(false, true, false)},
		{Name: "hack_acknowledge_checklist_item", Title: "Acknowledge a checklist item", Description: "Explicitly confirm o.rubric-ready, your j.rubric, or your j.conflicts review. Ask first. Rubric items need the current checklist version; conflicts are unversioned.", InputSchema: object(checklistWith(slug, map[string]any{"item_id": str("o.rubric-ready, j.rubric or j.conflicts."), "version": str("Current rubric item's version; omit for conflicts.")}), "slug", "item_id"), Annotations: writes(false, true, false)},
	}
	for i := range tools {
		name := tools[i].Name
		tools[i].OutputSchema = outObject(map[string]any{"can": hackAbilitiesSchema(), "checklist": hackChecklistSchema()}, "can", "checklist")
		tools[i].policy = toolPolicy{recordsPins: true}
		tools[i].run = func(c *call, args map[string]any) (output, error) {
			slug, err := eventSlug(args)
			if err != nil {
				return output{}, err
			}
			path := "/v1/hack/events/" + url.PathEscape(slug) + "/checklist"
			method := http.MethodGet
			var body []byte
			if name == "hack_get_checklist" {
				if role, ok, e := presentString(args, "role"); e != nil {
					return output{}, e
				} else if ok {
					path += "?role=" + url.QueryEscape(role)
				}
			} else {
				item, err := stringArg(args, "item_id")
				if err != nil {
					return output{}, err
				}
				if name == "hack_set_checklist_not_needed" {
					marked, ok := args["not_needed"].(bool)
					if !ok {
						return output{}, fmt.Errorf("not_needed must be a boolean")
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
						return output{}, e
					} else if ok {
						payload["version"] = version
					}
					body, _ = json.Marshal(payload)
				}
			}
			res := c.do(method, path, body, nil)
			if !res.ok() {
				return output{}, hackRestError(name, res)
			}
			var out map[string]any
			if err = json.Unmarshal(res.body, &out); err != nil {
				return output{}, err
			}
			return output{Text: jsonText(out), Structured: out}, nil
		}
	}
	return tools
}

func hackChecklistSchema() map[string]any {
	link := checklistNullableObject(outObject(map[string]any{"screen": outString("Screen."), "section": outString("Section."), "tab": checklistNullableString("Tab."), "href": outString("Resolved destination.")}, "screen", "section", "tab", "href"))
	item := outObject(map[string]any{
		"id": outString("Stable item ID."), "label": outString("Task."), "state": outString("done, not_needed, optional or todo."), "optional": outBool("Optional recommendation."), "can_dismiss": outBool("May mark not needed."), "origin_stage": outString("First stage."), "group": outString("current, earlier_required, earlier_optional or history."), "actionable": outBool("Action is available."), "target_id": outString("Navigation target."), "link": link, "detail": outString("Facts and counts."), "count": map[string]any{"type": []string{"integer", "null"}, "description": "Completed count."}, "total": map[string]any{"type": []string{"integer", "null"}, "description": "Total count."}, "settled": outBool("Incomplete settled entry."), "blocked_reason": checklistNullableString("Why action is unavailable."), "version": outString("Opaque current rubric version."),
	}, "id", "label", "state", "optional", "can_dismiss", "origin_stage", "group", "actionable", "target_id", "link", "detail", "count", "total", "settled", "blocked_reason")
	next := checklistNullableObject(outObject(map[string]any{"kind": outString("Action kind."), "item_id": outString("Leading item."), "target_id": outString("Target."), "label": outString("Action label."), "link": link, "stage": outString("Stage to move to."), "confirmation_required": outBool("Ask for confirmation before acting."), "blocked_reason": checklistNullableString("Existing forward-action blocker."), "cue": map[string]any{"type": []string{"object", "null"}, "additionalProperties": true}, "deadline_passed": outBool("Entry deadline passed.")}, "kind", "label", "link", "confirmation_required", "blocked_reason"))
	return outObject(map[string]any{"role": outString("Checklist role."), "stage": outString("Event stage."), "map_version": outInteger("Navigation map version."), "items": outArray("Selected checklist rows.", item), "next": next}, "role", "stage", "map_version", "items", "next")
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
