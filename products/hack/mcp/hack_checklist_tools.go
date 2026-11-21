package hackmcp

import (
	"encoding/json"
	"fmt"
	"github.com/vsriram/simple-host/host/mcp"
	"net/http"
	"net/url"
)

func hackChecklistTools() []mcp.Tool {
	slug := map[string]any{"slug": mcp.Str("Event slug.")}
	tools := []mcp.Tool{
		{Name: "hack_get_checklist", Title: "Read event checklist", Description: "Read the membership role's checklist and next action. Organisers may also request judge. Records due entry deadline pins.", InputSchema: mcp.Object(withFields(slug, map[string]any{"role": map[string]any{"type": "string", "enum": []string{"organiser", "judge", "participant"}}}), "slug"), Annotations: mcp.Writes(false, true, false)},
		{Name: "hack_set_checklist_not_needed", Title: "Mark an optional item not needed", Description: "Set or restore a shared optional organiser checklist item. Ask first. Ended events refuse changes.", InputSchema: mcp.Object(withFields(slug, map[string]any{"item_id": mcp.Str("Optional organiser item ID."), "not_needed": map[string]any{"type": "boolean"}}), "slug", "item_id", "not_needed"), Annotations: mcp.Writes(true, true, false)},
		{Name: "hack_acknowledge_checklist_item", Title: "Acknowledge a checklist item", Description: "Explicitly confirm your j.rubric or j.conflicts review. Ask first. Rubric items need the current checklist version; conflicts are unversioned.", InputSchema: mcp.Object(withFields(slug, map[string]any{"item_id": map[string]any{"type": "string", "enum": []string{"j.rubric", "j.conflicts"}, "description": "j.rubric or j.conflicts."}, "version": mcp.Str("Current rubric item's version; omit for conflicts.")}), "slug", "item_id"), Annotations: mcp.Writes(false, true, false)},
	}
	runs := map[string]func(*mcp.Call, map[string]any) (mcp.Output, error){
		"hack_get_checklist": runGetChecklist, "hack_set_checklist_not_needed": runSetChecklistNotNeeded,
		"hack_acknowledge_checklist_item": runAcknowledgeChecklistItem,
	}
	for i := range tools {
		tools[i].OutputSchema = mcp.OutObject(map[string]any{"can": hackAbilitiesSchema(), "checklist": hackChecklistSchema()}, "can", "checklist")
		tools[i] = mcp.WithRun(mcp.WithPolicy(tools[i], mcp.RecordsPins()), runs[tools[i].Name])
	}
	return tools
}

func runGetChecklist(c *mcp.Call, args map[string]any) (mcp.Output, error) {
	slug, err := eventSlug(args)
	if err != nil {
		return mcp.Output{}, err
	}
	path := eventPath(slug, "/checklist")
	role, present, err := presentString(args, "role")
	if err != nil {
		return mcp.Output{}, err
	}
	if present {
		path += "?role=" + url.QueryEscape(role)
	}
	return checklistCall(c, "hack_get_checklist", http.MethodGet, path, nil)
}

func runSetChecklistNotNeeded(c *mcp.Call, args map[string]any) (mcp.Output, error) {
	slug, item, err := checklistItem(args)
	if err != nil {
		return mcp.Output{}, err
	}
	marked, present, err := mcp.PresentBool(args, "not_needed")
	if err == nil && !present {
		err = fmt.Errorf("not_needed must be true or false")
	}
	if err != nil {
		return mcp.Output{}, err
	}
	method := map[bool]string{true: http.MethodPost, false: http.MethodDelete}[marked]
	return checklistCall(c, "hack_set_checklist_not_needed", method, eventPath(slug, "/checklist/not-needed/"+url.PathEscape(item)), nil)
}

func runAcknowledgeChecklistItem(c *mcp.Call, args map[string]any) (mcp.Output, error) {
	slug, item, err := checklistItem(args)
	if err != nil {
		return mcp.Output{}, err
	}
	payload := map[string]any{}
	version, present, err := presentString(args, "version")
	if err != nil {
		return mcp.Output{}, err
	}
	if present {
		payload["version"] = version
	}
	body, _ := json.Marshal(payload)
	return checklistCall(c, "hack_acknowledge_checklist_item", http.MethodPost, eventPath(slug, "/checklist/acknowledgements/"+url.PathEscape(item)), body)
}

func checklistItem(args map[string]any) (slug, item string, err error) {
	if slug, err = eventSlug(args); err != nil {
		return "", "", err
	}
	item, err = mcp.StringArg(args, "item_id")
	return slug, item, err
}

func checklistCall(c *mcp.Call, name, method, path string, body []byte) (mcp.Output, error) {
	res := c.Do(method, path, body, nil)
	if !res.OK() {
		return mcp.Output{}, hackRestError(name, res)
	}
	var out map[string]any
	if err := json.Unmarshal(res.Body(), &out); err != nil {
		return mcp.Output{}, err
	}
	return mcp.Output{Text: mcp.JSONText(out), Structured: out}, nil
}

func hackChecklistSchema() map[string]any {
	link := checklistNullableObject(mcp.OutObject(map[string]any{"screen": mcp.OutString("Screen."), "section": mcp.OutString("Section."), "tab": checklistNullableString("Tab."), "href": mcp.OutString("Resolved destination.")}, "screen", "section", "tab", "href"))
	item := mcp.OutObject(map[string]any{
		"id": mcp.OutString("Stable item ID."), "label": mcp.OutString("Task."), "state": mcp.OutString("done, not_needed, optional or todo."), "optional": mcp.OutBool("Optional recommendation."), "can_dismiss": mcp.OutBool("May mark not needed."), "origin_stage": mcp.OutString("First stage."), "group": mcp.OutString("current, earlier_required, earlier_optional or history."), "actionable": mcp.OutBool("Action is available."), "target_id": mcp.OutString("Navigation target."), "link": link, "detail": mcp.OutString("Facts and counts."), "count": map[string]any{"type": []string{"integer", "null"}, "description": "Completed count."}, "total": map[string]any{"type": []string{"integer", "null"}, "description": "Total count."}, "settled": mcp.OutBool("Incomplete settled entry."), "blocked_reason": checklistNullableString("Why action is unavailable."), "version": mcp.OutString("Opaque current rubric version."),
	}, "id", "label", "state", "optional", "can_dismiss", "origin_stage", "group", "actionable", "target_id", "link", "detail", "count", "total", "settled", "blocked_reason")
	next := checklistNullableObject(mcp.OutObject(map[string]any{"kind": mcp.OutString("Action kind."), "item_id": mcp.OutString("Leading item."), "target_id": mcp.OutString("Target."), "label": mcp.OutString("Action label."), "link": link, "stage": mcp.OutString("Stage to move to."), "confirmation_required": mcp.OutBool("Ask for confirmation before acting."), "blocked_reason": checklistNullableString("Existing forward-action blocker."), "cue": map[string]any{"type": []string{"object", "null"}, "additionalProperties": true}, "deadline_passed": mcp.OutBool("Entry deadline passed.")}, "kind", "label", "link", "confirmation_required", "blocked_reason"))
	return mcp.OutObject(map[string]any{"role": mcp.OutString("Checklist role."), "stage": mcp.OutString("Event stage."), "map_version": mcp.OutInteger("Navigation map version."), "items": mcp.OutArray("Selected checklist rows.", item), "next": next}, "role", "stage", "map_version", "items", "next")
}

func checklistNullableObject(s map[string]any) map[string]any {
	out := withFields(s, nil)
	out["type"] = []string{"object", "null"}
	return out
}
func checklistNullableString(description string) map[string]any {
	return map[string]any{"type": []string{"string", "null"}, "description": description}
}
