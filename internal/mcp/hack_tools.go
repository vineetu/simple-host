package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
)

// HackTools is the hosted organiser surface. Names are prefixed so they do
// not collide with website tools. Each tool calls one existing /v1/hack
// route; the REST handlers validate and enforce role. Listed only for an
// event-management connection (CallerModeEvents).
func HackTools() []Tool {
	tools := []Tool{
		{
			Name:        "hack_list_events",
			Title:       "List my events",
			Description: "List every hackathon event this person is part of, with their role (organiser, participant or judge). Call this before changing an event. You can manage an event only when the role is organiser.",
			InputSchema: noArgs(),
			Annotations: readOnly(),
			run: func(c *call, _ map[string]any) (output, error) {
				res := c.do(http.MethodGet, "/v1/hack/events", nil, nil)
				if !res.ok() {
					return output{}, hackRestError("hack_list_events", res)
				}
				return hackListOutput(res.body)
			},
		},
		{
			Name:        "hack_check_event_name",
			Title:       "Check an event name",
			Description: "Check whether an event name is free, and the address it would have. Names are 3 to 39 lowercase letters, numbers or hyphens, and cannot start or end with a hyphen. Check before hack_create_event.",
			InputSchema: object(map[string]any{
				"slug": str("The event name to check."),
			}, "slug"),
			Annotations: readOnly(),
			run: func(c *call, args map[string]any) (output, error) {
				slug, err := eventSlug(args)
				if err != nil {
					return output{}, err
				}
				res := c.do(http.MethodGet, "/v1/hack/names/"+url.PathEscape(slug), nil, nil)
				if !res.ok() {
					return output{}, hackRestError("hack_check_event_name", res)
				}
				return hackNameOutput(slug, res.body)
			},
		},
		{
			Name:        "hack_create_event",
			Title:       "Create an event",
			Description: "Create a draft event for this person, who becomes its organiser. Ask them first. A draft starts with the starter rubric. The answer includes the join and judge links. This does not publish a website.",
			InputSchema: object(map[string]any{
				"slug":                  str("Event name: 3 to 39 lowercase letters, numbers or hyphens, not starting or ending with a hyphen."),
				"title":                 str("Event title, one line, up to 120 characters."),
				"organiser_name":        str("Name shown as the organiser, one line, up to 100 characters."),
				"organisation":          str("Organisation or community, optional, up to 120 characters."),
				"contact_email":         str("Contact email for the event."),
				"purpose":               str("What the event is for, up to 1000 characters."),
				"expected_participants": map[string]any{"type": "integer", "description": "How many participants are expected, from 1 to 100000."},
				"starts_at":             str("When the event starts. A date (2026-10-01) means midnight in time_zone, or send a date and time."),
				"ends_at":               str("When the event ends, optional. Must not be before starts_at."),
				"time_zone":             str("IANA time zone, optional. Defaults to UTC."),
			}, "slug", "title", "organiser_name", "contact_email", "purpose", "expected_participants", "starts_at"),
			Annotations: writes(false, false, true),
			run: func(c *call, args map[string]any) (output, error) {
				body, err := hackCreateBody(args)
				if err != nil {
					return output{}, err
				}
				raw, err := json.Marshal(body)
				if err != nil {
					return output{}, err
				}
				res := c.do(http.MethodPost, "/v1/hack/events", raw, nil)
				if !res.ok() {
					return output{}, hackRestError("hack_create_event", res)
				}
				return hackEventOutput("Created", res.body)
			},
		},
		{
			Name:        "hack_get_event",
			Title:       "Get an event",
			Description: "Read one event: its page, stage and settings. When this person is the organiser, the answer includes the join and judge links. Give those links exactly as returned.",
			InputSchema: object(map[string]any{"slug": str("The event name.")}, "slug"),
			Annotations: readOnly(),
			run: func(c *call, args map[string]any) (output, error) {
				slug, err := eventSlug(args)
				if err != nil {
					return output{}, err
				}
				res := c.do(http.MethodGet, "/v1/hack/events/"+url.PathEscape(slug), nil, nil)
				if !res.ok() {
					return output{}, hackRestError("hack_get_event", res)
				}
				return hackEventOutput("Event", res.body)
			},
		},
		{
			Name:  "hack_update_event",
			Title: "Update an event",
			Description: "Change an event's page text and settings. Sends only the fields you set, so omit anything that should stay as it is. " +
				"Does not change the stage; use hack_set_event_stage for that. Ask the person before overwriting page text. Organiser only.",
			InputSchema: object(map[string]any{
				"slug":                  str("The event name."),
				"title":                 str("New title, one line, up to 120 characters."),
				"tagline":               str("Short line under the title, up to 160 characters. Empty clears it."),
				"about":                 str("About text. Empty clears it."),
				"rules":                 str("Rules text. Empty clears it."),
				"prizes":                str("Prizes text. Empty clears it."),
				"coc_text":              str("The organiser's own code of conduct, added to the default. Empty clears it."),
				"time_zone":             str("IANA time zone. Empty means UTC."),
				"starts_at":             str("New start. A date means midnight in the event's time zone."),
				"ends_at":               str("New end. Empty clears it."),
				"team_size_max":         map[string]any{"type": "integer", "description": "Maximum people on a team, from 1 to 50."},
				"organiser_name":        str("Name shown as the organiser."),
				"organisation":          str("Organisation or community. Empty clears it."),
				"contact_email":         str("Contact email for the event."),
				"purpose":               str("What the event is for."),
				"expected_participants": map[string]any{"type": "integer", "description": "How many participants are expected, from 1 to 100000."},
			}, "slug"),
			Annotations: writes(false, true, false),
			run: func(c *call, args map[string]any) (output, error) {
				slug, err := eventSlug(args)
				if err != nil {
					return output{}, err
				}
				body, err := hackPatchBody(args)
				if err != nil {
					return output{}, err
				}
				raw, err := json.Marshal(body)
				if err != nil {
					return output{}, err
				}
				res := c.do(http.MethodPatch, "/v1/hack/events/"+url.PathEscape(slug), raw, nil)
				if !res.ok() {
					return output{}, hackRestError("hack_update_event", res)
				}
				return hackEventOutput("Updated", res.body)
			},
		},
		{
			Name:  "hack_set_event_stage",
			Title: "Set an event's stage",
			Description: "Set an event's stage: draft, open, building, closed, judging, results or archived. " +
				"Ask the person first. closed, judging and results close submissions. archived ends the event and cannot be undone. Organiser only.",
			InputSchema: object(map[string]any{
				"slug": str("The event name."),
				"stage": map[string]any{
					"type":        "string",
					"enum":        []string{"draft", "open", "building", "closed", "judging", "results", "archived"},
					"description": "The stage to set. archived ends the event.",
				},
			}, "slug", "stage"),
			Annotations: writes(true, true, true),
			run: func(c *call, args map[string]any) (output, error) {
				slug, err := eventSlug(args)
				if err != nil {
					return output{}, err
				}
				stage, err := stringArg(args, "stage")
				if err != nil {
					return output{}, err
				}
				raw, err := json.Marshal(map[string]string{"stage": stage})
				if err != nil {
					return output{}, err
				}
				res := c.do(http.MethodPost, "/v1/hack/events/"+url.PathEscape(slug)+"/stage", raw, nil)
				if !res.ok() {
					return output{}, hackRestError("hack_set_event_stage", res)
				}
				return hackEventOutput("Stage", res.body)
			},
		},
		{
			Name:        "hack_get_rubric",
			Title:       "Get the rubric",
			Description: "Read an event's judging rubric: each criterion's name, description, weight and maximum points. Organiser or judge.",
			InputSchema: object(map[string]any{"slug": str("The event name.")}, "slug"),
			Annotations: readOnly(),
			run: func(c *call, args map[string]any) (output, error) {
				slug, err := eventSlug(args)
				if err != nil {
					return output{}, err
				}
				res := c.do(http.MethodGet, "/v1/hack/events/"+url.PathEscape(slug)+"/rubric", nil, nil)
				if !res.ok() {
					return output{}, hackRestError("hack_get_rubric", res)
				}
				return hackRubricOutput(slug, res.body)
			},
		},
		{
			Name:  "hack_set_rubric",
			Title: "Replace the rubric",
			Description: "Replace an event's whole rubric. This overwrites every criterion and deletes existing scores and comments. Explain that consequence and get confirmation before replacing a scored rubric. " +
				"1 to 10 criteria. Each weight is a whole number from 0 to 100 and the weights add up to 100. max_points is a whole number from 1 to 10. Organiser only. Refused while judging is locked.",
			InputSchema: object(map[string]any{
				"slug": str("The event name."),
				"criteria": map[string]any{
					"type":        "array",
					"description": "The criteria, in display order. This list replaces the current rubric.",
					"items": object(map[string]any{
						"name":        str("Criterion name, up to 80 characters."),
						"description": str("What judges are scoring, optional, up to 300 characters."),
						"weight":      map[string]any{"type": "integer", "description": "Whole number from 0 to 100. All weights must add up to 100."},
						"max_points":  map[string]any{"type": "integer", "description": "Whole number from 1 to 10."},
					}, "name", "weight", "max_points"),
				},
			}, "slug", "criteria"),
			Annotations: writes(true, true, false),
			run: func(c *call, args map[string]any) (output, error) {
				slug, err := eventSlug(args)
				if err != nil {
					return output{}, err
				}
				criteria, err := hackCriteria(args)
				if err != nil {
					return output{}, err
				}
				raw, err := json.Marshal(map[string]any{"criteria": criteria})
				if err != nil {
					return output{}, err
				}
				res := c.do(http.MethodPut, "/v1/hack/events/"+url.PathEscape(slug)+"/rubric", raw, nil)
				if !res.ok() {
					return output{}, hackRestError("hack_set_rubric", res)
				}
				return hackRubricOutput(slug, res.body)
			},
		},
		{
			Name:        "hack_export_scores",
			Title:       "Export scores",
			Description: "Download the event's scores as CSV (team, judge, criterion, points, max points, comment). Organiser only. Give the file to the person; do not post it publicly.",
			InputSchema: object(map[string]any{"slug": str("The event name.")}, "slug"),
			Annotations: readOnly(),
			run: func(c *call, args map[string]any) (output, error) {
				return hackExport(c, args, "scores")
			},
		},
		{
			Name:        "hack_export_results",
			Title:       "Export results",
			Description: "Download the event's results as CSV (team, total, rank, tied, judges scored). Uses the published results when there are some, otherwise the current totals. Organiser only. Give the file to the person; do not post it publicly.",
			InputSchema: object(map[string]any{"slug": str("The event name.")}, "slug"),
			Annotations: readOnly(),
			run: func(c *call, args map[string]any) (output, error) {
				return hackExport(c, args, "results")
			},
		},
	}
	schemas := hackOutputSchemas()
	for i := range tools {
		tools[i].OutputSchema = schemas[tools[i].Name]
	}
	return tools
}

func eventSlug(args map[string]any) (string, error) {
	slug, err := stringArg(args, "slug")
	if err != nil {
		return "", err
	}
	return strings.ToLower(slug), nil
}

func hackCreateBody(args map[string]any) (map[string]any, error) {
	slug, err := eventSlug(args)
	if err != nil {
		return nil, err
	}
	title, err := stringArg(args, "title")
	if err != nil {
		return nil, err
	}
	organiser, err := stringArg(args, "organiser_name")
	if err != nil {
		return nil, err
	}
	email, err := stringArg(args, "contact_email")
	if err != nil {
		return nil, err
	}
	purpose, err := stringArg(args, "purpose")
	if err != nil {
		return nil, err
	}
	n, _, err := wholeNumber(args, "expected_participants", true, math.MinInt32, math.MaxInt32)
	if err != nil {
		return nil, err
	}
	starts, err := stringArg(args, "starts_at")
	if err != nil {
		return nil, err
	}
	body := map[string]any{
		"slug": slug, "title": title, "organiser_name": organiser,
		"contact_email": email, "purpose": purpose,
		"expected_participants": n, "starts_at": starts,
	}
	for _, key := range []string{"organisation", "ends_at", "time_zone"} {
		value, present, err := presentString(args, key)
		if err != nil {
			return nil, err
		}
		if present && value != "" {
			body[key] = value
		}
	}
	return body, nil
}

func hackPatchBody(args map[string]any) (map[string]any, error) {
	body := map[string]any{}
	for _, key := range []string{
		"title", "tagline", "about", "rules", "prizes", "coc_text", "time_zone",
		"starts_at", "ends_at", "organiser_name", "organisation", "contact_email", "purpose",
	} {
		value, present, err := presentString(args, key)
		if err != nil {
			return nil, err
		}
		if present {
			body[key] = value
		}
	}
	for _, key := range []string{"team_size_max", "expected_participants"} {
		n, present, err := wholeNumber(args, key, false, math.MinInt32, math.MaxInt32)
		if err != nil {
			return nil, err
		}
		if present {
			body[key] = n
		}
	}
	if len(body) == 0 {
		return nil, errors.New("hack_update_event needs at least one field to change")
	}
	return body, nil
}

func hackCriteria(args map[string]any) ([]any, error) {
	raw, present := args["criteria"]
	if !present || raw == nil {
		return nil, errors.New("criteria is required")
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, errors.New("criteria must be a list of criteria")
	}
	if len(list) == 0 {
		return nil, errors.New("criteria must contain at least one criterion")
	}
	allowed := map[string]bool{"name": true, "description": true, "weight": true, "max_points": true}
	out := make([]any, 0, len(list))
	for i, item := range list {
		obj, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("criteria[%d] must be an object", i)
		}
		for key := range obj {
			if !allowed[key] {
				return nil, fmt.Errorf("criteria[%d] has unexpected field %q", i, key)
			}
		}
		name, err := stringArg(obj, "name")
		if err != nil {
			return nil, fmt.Errorf("criteria[%d]: %w", i, err)
		}
		weight, _, err := wholeNumber(obj, "weight", true, math.MinInt32, math.MaxInt32)
		if err != nil {
			return nil, fmt.Errorf("criteria[%d]: %w", i, err)
		}
		points, _, err := wholeNumber(obj, "max_points", true, math.MinInt32, math.MaxInt32)
		if err != nil {
			return nil, fmt.Errorf("criteria[%d]: %w", i, err)
		}
		one := map[string]any{"name": name, "weight": weight, "max_points": points}
		if _, present := obj["description"]; present {
			desc, _, err := presentString(obj, "description")
			if err != nil {
				return nil, fmt.Errorf("criteria[%d]: %w", i, err)
			}
			one["description"] = desc
		}
		out = append(out, one)
	}
	return out, nil
}

// presentString reports whether key was sent. An empty string is kept: an
// update uses it to clear a field. nil is not a string.
func presentString(args map[string]any, key string) (string, bool, error) {
	raw, present := args[key]
	if !present {
		return "", false, nil
	}
	if raw == nil {
		return "", false, fmt.Errorf("%s must be a string", key)
	}
	value, ok := raw.(string)
	if !ok {
		return "", false, fmt.Errorf("%s must be a string, got %T", key, raw)
	}
	return strings.TrimSpace(value), true, nil
}

func hackExport(c *call, args map[string]any, kind string) (output, error) {
	tool := "hack_export_" + kind
	slug, err := eventSlug(args)
	if err != nil {
		return output{}, err
	}
	res := c.do(http.MethodGet, "/v1/hack/events/"+url.PathEscape(slug)+"/export/"+kind+".csv", nil, nil)
	if !res.ok() {
		return output{}, hackRestError(tool, res)
	}
	return hackCSVOutput(slug, kind, res)
}

const hackCSVCap = 200 << 10

func hackCSVOutput(slug, kind string, res upstreamResult) (output, error) {
	filename := csvFilename(res.header, slug+"-"+kind+".csv")
	csvText := string(res.body)
	truncated := false
	if len(csvText) > hackCSVCap {
		csvText = csvText[:hackCSVCap]
		truncated = true
	}
	out := map[string]any{"slug": slug, "filename": filename, "csv": csvText, "truncated": truncated}
	text := filename + "\n" + csvText
	if truncated {
		text += "\n[truncated]"
	}
	return output{Text: text, Structured: out}, nil
}

func csvFilename(h http.Header, fallback string) string {
	d := h.Get("Content-Disposition")
	const key = `filename="`
	i := strings.Index(d, key)
	if i < 0 {
		return fallback
	}
	rest := d[i+len(key):]
	j := strings.IndexByte(rest, '"')
	if j <= 0 {
		return fallback
	}
	return rest[:j]
}

func hackRestError(tool string, u upstreamResult) error {
	msg, code := upstreamMessage(u)
	switch code {
	case "invalid_name":
		return fmt.Errorf("%s failed (HTTP %d): %s [%s]\nEvent names are 3 to 39 lowercase letters, numbers or hyphens, and cannot start or end with a hyphen. Correct the name and call again.", tool, u.status, msg, code)
	case "name_reserved":
		return fmt.Errorf("%s failed (HTTP %d): %s [%s]\nThat event name is reserved. Ask the person for a different name.", tool, u.status, msg, code)
	default:
		return restError(tool, u)
	}
}

func upstreamMessage(u upstreamResult) (string, string) {
	var payload struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	_ = json.Unmarshal(u.body, &payload)
	msg := payload.Error
	if msg == "" {
		msg = strings.TrimSpace(string(u.body))
		if len(msg) > 300 {
			msg = msg[:300]
		}
	}
	if msg == "" {
		msg = http.StatusText(u.status)
	}
	return msg, payload.Code
}

type hackEventView struct {
	Event struct {
		Slug          string   `json:"slug"`
		Title         string   `json:"title"`
		Tagline       string   `json:"tagline"`
		About         string   `json:"about"`
		Rules         string   `json:"rules"`
		Prizes        string   `json:"prizes"`
		CocText       string   `json:"coc_text"`
		Stage         string   `json:"stage"`
		TimeZone      string   `json:"time_zone"`
		StartsAt      *string  `json:"starts_at"`
		EndsAt        *string  `json:"ends_at"`
		TeamSizeMax   int      `json:"team_size_max"`
		URL           string   `json:"url"`
		TakenDown     bool     `json:"taken_down"`
		StagesOffered []string `json:"stages_offered"`
	} `json:"event"`
	Role      string `json:"role"`
	Organiser *struct {
		OrganiserName        string `json:"organiser_name"`
		Organisation         string `json:"organisation"`
		ContactEmail         string `json:"contact_email"`
		Purpose              string `json:"purpose"`
		ExpectedParticipants *int   `json:"expected_participants"`
		JoinCode             string `json:"join_code"`
		JoinURL              string `json:"join_url"`
		JudgeCode            string `json:"judge_code"`
		JudgeURL             string `json:"judge_url"`
		Counts               struct {
			Participants int `json:"participants"`
			Teams        int `json:"teams"`
			Judges       int `json:"judges"`
			OnNoTeam     int `json:"on_no_team"`
		} `json:"counts"`
	} `json:"organiser"`
}

func hackEventOutput(verb string, body []byte) (output, error) {
	var view hackEventView
	if err := json.Unmarshal(body, &view); err != nil || view.Event.Slug == "" {
		return output{}, errors.New("the server returned an event this tool could not read")
	}
	offered := append([]string{}, view.Event.StagesOffered...)
	out := map[string]any{
		"slug":           view.Event.Slug,
		"title":          view.Event.Title,
		"tagline":        view.Event.Tagline,
		"about":          view.Event.About,
		"rules":          view.Event.Rules,
		"prizes":         view.Event.Prizes,
		"coc_text":       view.Event.CocText,
		"stage":          view.Event.Stage,
		"role":           view.Role,
		"url":            view.Event.URL,
		"time_zone":      view.Event.TimeZone,
		"team_size_max":  view.Event.TeamSizeMax,
		"taken_down":     view.Event.TakenDown,
		"stages_offered": offered,
		"starts_at":      nil,
		"ends_at":        nil,
	}
	if view.Event.StartsAt != nil {
		out["starts_at"] = *view.Event.StartsAt
	}
	if view.Event.EndsAt != nil {
		out["ends_at"] = *view.Event.EndsAt
	}
	text := fmt.Sprintf("%s %s (%s), stage %s. Role: %s.", verb, view.Event.Title, view.Event.Slug, view.Event.Stage, view.Role)
	if view.Event.URL != "" {
		text += " Address: " + view.Event.URL
	}
	if o := view.Organiser; o != nil {
		var expected any
		if o.ExpectedParticipants != nil {
			expected = *o.ExpectedParticipants
		}
		out["organiser"] = map[string]any{
			"organiser_name":        o.OrganiserName,
			"organisation":          o.Organisation,
			"contact_email":         o.ContactEmail,
			"purpose":               o.Purpose,
			"expected_participants": expected,
			"join_code":             o.JoinCode,
			"join_url":              o.JoinURL,
			"judge_code":            o.JudgeCode,
			"judge_url":             o.JudgeURL,
			"counts": map[string]any{
				"participants": o.Counts.Participants,
				"teams":        o.Counts.Teams,
				"judges":       o.Counts.Judges,
				"on_no_team":   o.Counts.OnNoTeam,
			},
		}
		text += "\nJoin link: " + o.JoinURL + "\nJudge link: " + o.JudgeURL
	}
	return output{Text: text, Structured: out}, nil
}

type hackListItem struct {
	Slug      string  `json:"slug"`
	Title     string  `json:"title"`
	Stage     string  `json:"stage"`
	Role      string  `json:"role"`
	URL       string  `json:"url"`
	ManageURL string  `json:"manage_url"`
	StartsAt  *string `json:"starts_at"`
	EndsAt    *string `json:"ends_at"`
	TakenDown bool    `json:"taken_down"`
	TimeZone  string  `json:"time_zone"`
}

func hackListOutput(body []byte) (output, error) {
	var rows []hackListItem
	if err := json.Unmarshal(body, &rows); err != nil {
		return output{}, errors.New("the server returned an event list this tool could not read")
	}
	items := make([]any, 0, len(rows))
	for _, row := range rows {
		item := map[string]any{
			"slug": row.Slug, "title": row.Title, "stage": row.Stage, "role": row.Role,
			"url": row.URL, "manage_url": row.ManageURL, "time_zone": row.TimeZone,
			"taken_down": row.TakenDown, "starts_at": nil, "ends_at": nil,
		}
		if row.StartsAt != nil {
			item["starts_at"] = *row.StartsAt
		}
		if row.EndsAt != nil {
			item["ends_at"] = *row.EndsAt
		}
		items = append(items, item)
	}
	out := map[string]any{"events": items, "count": len(items)}
	if len(items) == 0 {
		return output{Text: "This person is not part of any event yet. Create one with hack_create_event after they agree.", Structured: out}, nil
	}
	return output{Text: jsonText(out), Structured: out}, nil
}

func hackNameOutput(slug string, body []byte) (output, error) {
	var parsed struct {
		Address   string `json:"address"`
		Available bool   `json:"available"`
		Code      string `json:"code"`
		Error     string `json:"error"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return output{}, errors.New("the server returned a name check this tool could not read")
	}
	out := map[string]any{"slug": slug, "available": parsed.Available, "address": parsed.Address}
	text := slug + " is free. Address: " + parsed.Address
	if !parsed.Available {
		if parsed.Code != "" {
			out["code"] = parsed.Code
		}
		if parsed.Error != "" {
			out["error"] = parsed.Error
		}
		text = slug + " is not available"
		if parsed.Error != "" {
			text += ": " + parsed.Error
		}
		text += "."
	}
	return output{Text: text, Structured: out}, nil
}

type hackRubric struct {
	Criteria []struct {
		Position    int    `json:"position"`
		Name        string `json:"name"`
		Description string `json:"description"`
		Weight      int    `json:"weight"`
		MaxPoints   int    `json:"max_points"`
	} `json:"criteria"`
}

func hackRubricOutput(slug string, body []byte) (output, error) {
	var parsed hackRubric
	if err := json.Unmarshal(body, &parsed); err != nil {
		return output{}, errors.New("the server returned a rubric this tool could not read")
	}
	items := make([]any, 0, len(parsed.Criteria))
	for _, c := range parsed.Criteria {
		items = append(items, map[string]any{
			"position": c.Position, "name": c.Name, "description": c.Description,
			"weight": c.Weight, "max_points": c.MaxPoints,
		})
	}
	out := map[string]any{"slug": slug, "criteria": items}
	return output{Text: jsonText(out), Structured: out}, nil
}

func hackOutputSchemas() map[string]map[string]any {
	nullableString := func(desc string) map[string]any {
		return map[string]any{"type": []any{"string", "null"}, "description": desc}
	}
	nullableInteger := func(desc string) map[string]any {
		return map[string]any{"type": []any{"integer", "null"}, "description": desc}
	}
	event := outObject(map[string]any{
		"slug":           outString("The event name."),
		"title":          outString("The event title."),
		"tagline":        outString("Short line under the title. Empty when unset."),
		"about":          outString("About text. Empty when unset."),
		"rules":          outString("Rules text. Empty when unset."),
		"prizes":         outString("Prizes text. Empty when unset."),
		"coc_text":       outString("The organiser's own code of conduct. Empty when unset."),
		"stage":          outString("draft, open, building, closed, judging, results or archived."),
		"role":           outString("This person's role: organiser, participant or judge."),
		"url":            outString("The event's public address."),
		"time_zone":      outString("IANA time zone."),
		"team_size_max":  outInteger("Maximum people on a team."),
		"taken_down":     outBool("Whether the operator has taken the event down."),
		"stages_offered": outArray("Stages hack_set_event_stage accepts.", outString("A stage name.")),
		"starts_at":      nullableString("Start time, or null when unset."),
		"ends_at":        nullableString("End time, or null when unset."),
		"organiser": withDescription(outObject(map[string]any{
			"organiser_name":        outString("Name shown as the organiser."),
			"organisation":          outString("Organisation or community. Empty when unset."),
			"contact_email":         outString("Contact email."),
			"purpose":               outString("What the event is for."),
			"expected_participants": nullableInteger("How many participants are expected, or null."),
			"join_code":             outString("Code in the join link."),
			"join_url":              outString("Link participants use to join. Give this to the person exactly."),
			"judge_code":            outString("Code in the judge link."),
			"judge_url":             outString("Link judges use to join. Give this to the person exactly."),
			"counts": outObject(map[string]any{
				"participants": outInteger("Participants, not counting organisers or judges."),
				"teams":        outInteger("Teams."),
				"judges":       outInteger("Judges."),
				"on_no_team":   outInteger("Participants who have not joined a team."),
			}, "participants", "teams", "judges", "on_no_team"),
		}, "organiser_name", "organisation", "contact_email", "purpose", "expected_participants", "join_code", "join_url", "judge_code", "judge_url", "counts"), "Present only when this person is the organiser."),
	}, "slug", "title", "tagline", "about", "rules", "prizes", "coc_text", "stage", "role", "url", "time_zone", "team_size_max", "taken_down", "stages_offered", "starts_at", "ends_at")
	listItem := outObject(map[string]any{
		"slug":       outString("The event name."),
		"title":      outString("The event title."),
		"stage":      outString("The event stage."),
		"role":       outString("This person's role on the event."),
		"url":        outString("The event's public address."),
		"manage_url": outString("The organiser's page for the event."),
		"time_zone":  outString("IANA time zone."),
		"taken_down": outBool("Whether the operator has taken the event down."),
		"starts_at":  nullableString("Start time, or null when unset."),
		"ends_at":    nullableString("End time, or null when unset."),
	}, "slug", "title", "stage", "role", "url", "manage_url", "time_zone", "taken_down", "starts_at", "ends_at")
	name := outObject(map[string]any{
		"slug":      outString("The name that was checked."),
		"available": outBool("True when the name can be used for a new event."),
		"address":   outString("The address the event would have."),
		"code":      outString("Why the name is unavailable, when available is false."),
		"error":     outString("The same reason in a sentence, when available is false."),
	}, "slug", "available", "address")
	rubric := outObject(map[string]any{
		"slug": outString("The event name."),
		"criteria": outArray("The rubric, in display order.", outObject(map[string]any{
			"position":    outInteger("Display order, starting at 1."),
			"name":        outString("Criterion name."),
			"description": outString("What judges are scoring. Empty when unset."),
			"weight":      outInteger("Weight, a whole number. All weights add up to 100."),
			"max_points":  outInteger("Maximum points a judge can give, from 1 to 10."),
		}, "position", "name", "description", "weight", "max_points")),
	}, "slug", "criteria")
	csv := outObject(map[string]any{
		"slug":      outString("The event name."),
		"filename":  outString("Suggested file name."),
		"csv":       outString("The CSV text, truncated when truncated is true."),
		"truncated": outBool("True when csv was cut off to fit the reply."),
	}, "slug", "filename", "csv", "truncated")
	return map[string]map[string]any{
		"hack_list_events":      outObject(map[string]any{"events": outArray("Events this person is part of.", listItem), "count": outInteger("How many events.")}, "events", "count"),
		"hack_check_event_name": name,
		"hack_create_event":     event,
		"hack_get_event":        event,
		"hack_update_event":     event,
		"hack_set_event_stage":  event,
		"hack_get_rubric":       rubric,
		"hack_set_rubric":       rubric,
		"hack_export_scores":    csv,
		"hack_export_results":   csv,
	}
}
