// famHackEvents declares events, members, teams, connections and organiser administration.
package hackmcp

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/vsriram/simple-host/internal/mcp"
	"math"
	"net/http"
	"net/url"
)

func hackBaseEventTools() []mcp.Tool {
	schemas := hackEventSchemas()
	tools := append([]mcp.Tool{memberTool("hack_get_navigation", "Read navigation", "Read the shared Hack screens and destination map.", http.MethodGet, "/v1/hack/navigation", mcp.NoArgs(), nil, mcp.ReadOnly())}, declare([]decl{
		{
			OutputSchema: schemas.list,
			Name:         "hack_list_events",
			Title:        "List my events",
			Description:  "List every hackathon event this person is part of, with their role (organiser, participant or judge). Call this before changing an event. You can manage an event only when the role is organiser.",
			InputSchema:  mcp.NoArgs(),
			Annotations:  mcp.ReadOnly(),
			run: func(c *mcp.Call, _ map[string]any) (mcp.Output, error) {
				res := c.Do(http.MethodGet, "/v1/hack/events", nil, nil)
				if !res.OK() {
					return mcp.Output{}, hackRestError("hack_list_events", res)
				}
				return hackListOutput(res.Body())
			},
		},
		{
			OutputSchema: schemas.name,
			Name:         "hack_check_event_name",
			Title:        "Check an event name",
			Description:  "Check whether an event name is free, and the address it would have. Names are 3 to 39 lowercase letters, numbers or hyphens, and cannot start or end with a hyphen. Check before hack_create_event.",
			InputSchema: mcp.Object(map[string]any{
				"slug": mcp.Str("The event name to check."),
			}, "slug"),
			Annotations: hackPublicReadOnly(),
			run: func(c *mcp.Call, args map[string]any) (mcp.Output, error) {
				slug, err := eventSlug(args)
				if err != nil {
					return mcp.Output{}, err
				}
				res := c.Do(http.MethodGet, "/v1/hack/names/"+url.PathEscape(slug), nil, nil)
				if !res.OK() {
					return mcp.Output{}, hackRestError("hack_check_event_name", res)
				}
				return hackNameOutput(slug, res.Body())
			},
		},
		{
			policy:       toolPolicy{analytics: analyticsPolicy{secretText: true}},
			OutputSchema: schemas.event,
			Name:         "hack_create_event",
			Title:        "Create an event",
			Description:  "Create a draft event for this person, who becomes its organiser. Ask them first. A draft starts with the starter rubric. The answer includes the join and judge links. This does not publish a website.",
			InputSchema: mcp.Object(map[string]any{
				"slug":                  mcp.Str("Event name: 3 to 39 lowercase letters, numbers or hyphens, not starting or ending with a hyphen."),
				"title":                 mcp.Str("Event title, one line, up to 120 characters."),
				"organiser_name":        mcp.Str("Public event organiser name, one line, up to 100 characters. Membership uses the account name; if empty, this fills it once."),
				"organisation":          mcp.Str("The organiser's company, college or community, if they have one. Optional, up to 120 characters; leave this out rather than sending a placeholder."),
				"contact_email":         mcp.Str("Contact email for the event."),
				"purpose":               mcp.Str("What the event is for, up to 1000 characters."),
				"expected_participants": map[string]any{"type": "integer", "description": "How many participants are expected, from 1 to 100000."},
				"starts_at":             mcp.Str("When the event starts. A date (2026-10-01) means midnight in time_zone, or send a date and time."),
				"ends_at":               mcp.Str("When the event ends, optional. Must not be before starts_at."),
				"time_zone":             mcp.Str("IANA time zone, optional. Defaults to UTC."),
			}, "slug", "title", "organiser_name", "contact_email", "purpose", "expected_participants", "starts_at"),
			Annotations: mcp.Writes(false, false, true),
			run: func(c *mcp.Call, args map[string]any) (mcp.Output, error) {
				body, err := hackCreateBody(args)
				if err != nil {
					return mcp.Output{}, err
				}
				raw, err := json.Marshal(body)
				if err != nil {
					return mcp.Output{}, err
				}
				res := c.Do(http.MethodPost, "/v1/hack/events", raw, nil)
				if !res.OK() {
					return mcp.Output{}, hackRestError("hack_create_event", res)
				}
				return hackEventOutput("Created", res.Body())
			},
		},
		{
			policy:       toolPolicy{analytics: analyticsPolicy{secretText: true}},
			OutputSchema: schemas.event,
			Name:         "hack_get_event",
			Title:        "Get an event",
			Description:  "Read one event: its page, submissions_public, overall_prizes, stage, settings and membership checklist. Use checklist.next for guided work; confirm actions before writing. When this person is the organiser, the answer includes the join and judge links and next_step, the one forward move the website offers (its one button) for the current stage. next_step.cue is set when a schedule row lining up with that move has come (e.g. \"Kickoff is now. Open sign-up?\"): ask the organiser, never move the stage on your own. Give those links exactly as returned. Records any due submission deadline pins.",
			InputSchema:  mcp.Object(map[string]any{"slug": mcp.Str("The event name.")}, "slug"),
			Annotations:  mcp.Writes(false, true, false),
			run: func(c *mcp.Call, args map[string]any) (mcp.Output, error) {
				slug, err := eventSlug(args)
				if err != nil {
					return mcp.Output{}, err
				}
				res := c.Do(http.MethodGet, "/v1/hack/events/"+url.PathEscape(slug), nil, nil)
				if !res.OK() {
					return mcp.Output{}, hackRestError("hack_get_event", res)
				}
				return hackEventOutput("Event", res.Body())
			},
		},
		{
			policy:       toolPolicy{analytics: analyticsPolicy{secretText: true}},
			OutputSchema: schemas.event,
			Name:         "hack_update_event",
			Title:        "Update an event",
			Description: "Change an event's page text and settings, including overall_prizes for 1st/2nd/3rd. Sends only the fields you set, so omit anything that should stay as it is. " +
				"Does not change the stage; use hack_set_event_stage for that. Ask the person before overwriting page text. Organiser only.",
			InputSchema: mcp.Object(map[string]any{
				"slug":                mcp.Str("The event name."),
				"title":               mcp.Str("New title, one line, up to 120 characters."),
				"tagline":             mcp.Str("Short line under the title, up to 160 characters. Empty clears it."),
				"about":               mcp.Str("About text. Empty clears it."),
				"rules":               mcp.Str("Rules text. Empty clears it."),
				"prizes":              mcp.Str("Prizes text. Empty clears it."),
				"overall_prizes":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Up to three prizes by place: index 0 = 1st, 1 = 2nd, 2 = 3rd. Each is one trimmed line, up to 60 characters. Empty skips a place; trailing empty strings are trimmed. [] clears."},
				"coc_text":            mcp.Str("The organiser's own code of conduct, added to the default. Empty clears it."),
				"time_zone":           mcp.Str("IANA time zone. Empty means UTC."),
				"starts_at":           mcp.Str("New start. A date means midnight in the event's time zone."),
				"ends_at":             mcp.Str("New end. Empty clears it."),
				"submission_deadline": mcp.Str("Entry deadline in the event time zone. Empty clears it."),
				"judging_ends_at":     mcp.Str("When judging closes, in the event time zone. Empty clears it. Judges with unscored teams are reminded before this time, once set; with no judging_ends_at they get no reminder."),
				"entry_required":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Deprecated: use hack_set_entry_rules instead (presets, per-field required/optional/off/one_of, judge_first). Sending this still works: it sets required for exactly the listed fields and demotes the stored preset to custom."},
				"team_finding":        map[string]any{"type": "boolean", "description": "Enable private team finding. Codes keep working when off."},
				"applications_open":   map[string]any{"type": "boolean", "description": "Whether people without the join link can apply from Find a hackathon and the event page. On by default; turning it off stops new applications and keeps waiting ones. Ask the organiser before changing it."},
				"submissions_public":  map[string]any{"type": "boolean", "description": "Show submissions on the event page from Judging onward. Off by default; separate from gallery_open."},
				"gallery_open":        map[string]any{"type": "boolean", "description": "Whether the public entry gallery is open."},
				"theme": map[string]any{
					"type":        "string",
					"enum":        []string{"", "poster", "confetti", "boardroom", "garden", "terminal"},
					"description": "Page style for the public event page: poster, confetti, boardroom, garden, terminal, or an empty string for the classic look. The organiser's accent_color still drives buttons and links under every style.",
				},
				"team_size_max":         map[string]any{"type": "integer", "description": "Maximum people on a team, from 1 to 50."},
				"organiser_name":        mcp.Str("Public event organiser name. Membership uses the account name; if empty, this fills it once."),
				"organisation":          mcp.Str("The organiser's company, college or community, if they have one. Empty clears it; leave this out rather than sending a placeholder."),
				"contact_email":         mcp.Str("Contact email for the event."),
				"purpose":               mcp.Str("What the event is for."),
				"expected_participants": map[string]any{"type": "integer", "description": "How many participants are expected, from 1 to 100000."},
			}, "slug"),
			Annotations: mcp.Writes(true, true, true),
			run: func(c *mcp.Call, args map[string]any) (mcp.Output, error) {
				slug, err := eventSlug(args)
				if err != nil {
					return mcp.Output{}, err
				}
				body, err := hackPatchBody(args)
				if err != nil {
					return mcp.Output{}, err
				}
				raw, err := json.Marshal(body)
				if err != nil {
					return mcp.Output{}, err
				}
				res := c.Do(http.MethodPatch, "/v1/hack/events/"+url.PathEscape(slug), raw, nil)
				if !res.OK() {
					return mcp.Output{}, hackRestError("hack_update_event", res)
				}
				return hackEventOutput("Updated", res.Body())
			},
		},
		{
			policy:       toolPolicy{analytics: analyticsPolicy{secretText: true}},
			OutputSchema: schemas.event,
			Name:         "hack_set_event_stage",
			Title:        "Set an event's stage",
			Description: "Set an event's stage: draft, open, building, closed, judging, results or archived. " +
				"Moving to Judging locks the judging setup and, in Automatic mode, assigns judges. Ask the person first. closed, judging and results close entries. results locks judging; moving back to judging reopens scoring. archived ends the event and cannot be undone. Organiser only. " +
				"After a successful change, offer a short announcement through hack_post_announcement describing the move, which the person can edit or skip entirely, and only email participants if they ask for that. " +
				"For archived, offer and post it before you archive, not after: an ended event takes no announcements.",
			InputSchema: mcp.Object(map[string]any{
				"slug": mcp.Str("The event name."),
				"stage": map[string]any{
					"type":        "string",
					"enum":        []string{"draft", "open", "building", "closed", "judging", "results", "archived"},
					"description": "The stage to set. archived ends the event.",
				},
			}, "slug", "stage"),
			Annotations: mcp.Writes(true, true, true),
			run: func(c *mcp.Call, args map[string]any) (mcp.Output, error) {
				slug, err := eventSlug(args)
				if err != nil {
					return mcp.Output{}, err
				}
				stage, err := mcp.StringArg(args, "stage")
				if err != nil {
					return mcp.Output{}, err
				}
				raw, err := json.Marshal(map[string]string{"stage": stage})
				if err != nil {
					return mcp.Output{}, err
				}
				res := c.Do(http.MethodPost, "/v1/hack/events/"+url.PathEscape(slug)+"/stage", raw, nil)
				if !res.OK() {
					return mcp.Output{}, hackRestError("hack_set_event_stage", res)
				}
				return hackEventOutput("Stage", res.Body())
			},
		},
	})...)
	return tools
}

func hackCreateBody(args map[string]any) (map[string]any, error) {
	slug, err := eventSlug(args)
	if err != nil {
		return nil, err
	}
	title, err := mcp.StringArg(args, "title")
	if err != nil {
		return nil, err
	}
	organiser, err := mcp.StringArg(args, "organiser_name")
	if err != nil {
		return nil, err
	}
	email, err := mcp.StringArg(args, "contact_email")
	if err != nil {
		return nil, err
	}
	purpose, err := mcp.StringArg(args, "purpose")
	if err != nil {
		return nil, err
	}
	n, _, err := mcp.WholeNumber(args, "expected_participants", true, math.MinInt32, math.MaxInt32)
	if err != nil {
		return nil, err
	}
	starts, err := mcp.StringArg(args, "starts_at")
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
		"starts_at", "ends_at", "submission_deadline", "judging_ends_at", "organiser_name", "organisation", "contact_email", "purpose", "theme",
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
		n, present, err := mcp.WholeNumber(args, key, false, math.MinInt32, math.MaxInt32)
		if err != nil {
			return nil, err
		}
		if present {
			body[key] = n
		}
	}
	if raw, present := args["overall_prizes"]; present {
		body["overall_prizes"] = raw
	}
	if raw, present := args["entry_required"]; present {
		list, ok := raw.([]any)
		if !ok {
			return nil, errors.New("entry_required must be a list of field names")
		}
		for _, field := range list {
			if _, ok := field.(string); !ok {
				return nil, errors.New("entry_required must be a list of field names")
			}
		}
		body["entry_required"] = list
	}
	if raw, present := args["submissions_public"]; present {
		value, ok := raw.(bool)
		if !ok {
			return nil, errors.New("submissions_public must be true or false")
		}
		body["submissions_public"] = value
	}
	if raw, present := args["gallery_open"]; present {
		open, ok := raw.(bool)
		if !ok {
			return nil, errors.New("gallery_open must be true or false")
		}
		body["gallery_open"] = open
	}
	if value, present := args["team_finding"]; present {
		b, ok := value.(bool)
		if !ok {
			return nil, errors.New("team_finding must be true or false")
		}
		body["team_finding"] = b
	}
	if value, present := args["applications_open"]; present {
		b, ok := value.(bool)
		if !ok {
			return nil, errors.New("applications_open must be true or false")
		}
		body["applications_open"] = b
	}
	if len(body) == 0 {
		return nil, errors.New("hack_update_event needs at least one field to change")
	}
	return body, nil
}

type hackEventView struct {
	Checklist any `json:"checklist"`
	Event     struct {
		Slug              string   `json:"slug"`
		Title             string   `json:"title"`
		Tagline           string   `json:"tagline"`
		About             string   `json:"about"`
		Rules             string   `json:"rules"`
		Prizes            string   `json:"prizes"`
		OverallPrizes     []string `json:"overall_prizes"`
		SubmissionsPublic bool     `json:"submissions_public"`
		CocText           string   `json:"coc_text"`
		Theme             string   `json:"theme"`
		Stage             string   `json:"stage"`
		TimeZone          string   `json:"time_zone"`
		StartsAt          *string  `json:"starts_at"`
		EndsAt            *string  `json:"ends_at"`
		JudgingEndsAt     *string  `json:"judging_ends_at"`
		TeamSizeMax       int      `json:"team_size_max"`
		TeamFinding       bool     `json:"team_finding"`
		URL               string   `json:"url"`
		TakenDown         bool     `json:"taken_down"`
		StagesOffered     []string `json:"stages_offered"`
	} `json:"event"`
	Role     string          `json:"role"`
	Can      map[string]bool `json:"can"`
	NextStep *struct {
		Kind           string  `json:"kind"`
		Stage          string  `json:"stage"`
		Label          string  `json:"label"`
		DeadlinePassed bool    `json:"deadline_passed"`
		Blocked        *string `json:"blocked"`
		Cue            *struct {
			Key     string  `json:"key"`
			Title   string  `json:"title"`
			StartAt string  `json:"start_at"`
			EndAt   *string `json:"end_at"`
			Fixed   bool    `json:"fixed"`
			Now     bool    `json:"now"`
			Text    string  `json:"text"`
		} `json:"cue"`
	} `json:"next_step"`
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

func hackEventOutput(verb string, body []byte) (mcp.Output, error) {
	var view hackEventView
	if err := json.Unmarshal(body, &view); err != nil || view.Event.Slug == "" {
		return mcp.Output{}, errors.New("the server returned an event this tool could not read")
	}
	offered := append([]string{}, view.Event.StagesOffered...)
	out := map[string]any{
		"slug":               view.Event.Slug,
		"title":              view.Event.Title,
		"tagline":            view.Event.Tagline,
		"about":              view.Event.About,
		"rules":              view.Event.Rules,
		"prizes":             view.Event.Prizes,
		"overall_prizes":     append([]string{}, view.Event.OverallPrizes...),
		"submissions_public": view.Event.SubmissionsPublic,
		"coc_text":           view.Event.CocText,
		"theme":              view.Event.Theme,
		"stage":              view.Event.Stage,
		"role":               view.Role,
		"url":                view.Event.URL,
		"time_zone":          view.Event.TimeZone,
		"team_size_max":      view.Event.TeamSizeMax,
		"team_finding":       view.Event.TeamFinding,
		"taken_down":         view.Event.TakenDown,
		"stages_offered":     offered,
		"starts_at":          nil,
		"ends_at":            nil,
		"judging_ends_at":    nil,
	}
	if view.Can != nil {
		out["can"] = view.Can
	}
	if view.Checklist != nil {
		out["checklist"] = view.Checklist
	}
	if view.Event.StartsAt != nil {
		out["starts_at"] = *view.Event.StartsAt
	}
	if view.Event.EndsAt != nil {
		out["ends_at"] = *view.Event.EndsAt
	}
	if view.Event.JudgingEndsAt != nil {
		out["judging_ends_at"] = *view.Event.JudgingEndsAt
	}
	if n := view.NextStep; n != nil {
		var blocked any
		if n.Blocked != nil {
			blocked = *n.Blocked
		}
		var cue any
		if c := n.Cue; c != nil {
			var end any
			if c.EndAt != nil {
				end = *c.EndAt
			}
			cue = map[string]any{"key": c.Key, "title": c.Title, "start_at": c.StartAt, "end_at": end, "fixed": c.Fixed, "now": c.Now, "text": c.Text}
		}
		out["next_step"] = map[string]any{
			"kind":            n.Kind,
			"stage":           n.Stage,
			"label":           n.Label,
			"deadline_passed": n.DeadlinePassed,
			"blocked":         blocked,
			"cue":             cue,
		}
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
	return mcp.Output{Text: text, Structured: out}, nil
}

type hackListItem struct {
	Slug      string          `json:"slug"`
	Title     string          `json:"title"`
	Stage     string          `json:"stage"`
	Role      string          `json:"role"`
	Can       map[string]bool `json:"can"`
	EndedAt   *string         `json:"ended_at"`
	URL       string          `json:"url"`
	ManageURL string          `json:"manage_url"`
	StartsAt  *string         `json:"starts_at"`
	EndsAt    *string         `json:"ends_at"`
	TakenDown bool            `json:"taken_down"`
	TimeZone  string          `json:"time_zone"`
}

func hackListOutput(body []byte) (mcp.Output, error) {
	var rows []hackListItem
	if err := json.Unmarshal(body, &rows); err != nil {
		return mcp.Output{}, errors.New("the server returned an event list this tool could not read")
	}
	items := make([]any, 0, len(rows))
	for _, row := range rows {
		item := map[string]any{
			"slug": row.Slug, "title": row.Title, "stage": row.Stage, "role": row.Role,
			"ended_at": row.EndedAt,
			"url":      row.URL, "manage_url": row.ManageURL, "time_zone": row.TimeZone,
			"taken_down": row.TakenDown, "starts_at": nil, "ends_at": nil,
		}
		if row.Can != nil {
			item["can"] = row.Can
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
		return mcp.Output{Text: "This person is not part of any event yet. Create one with hack_create_event after they agree.", Structured: out}, nil
	}
	return mcp.Output{Text: mcp.JSONText(out), Structured: out}, nil
}

func hackNameOutput(slug string, body []byte) (mcp.Output, error) {
	var parsed struct {
		Address   string `json:"address"`
		Available bool   `json:"available"`
		Code      string `json:"code"`
		Error     string `json:"error"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return mcp.Output{}, errors.New("the server returned a name check this tool could not read")
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
	return mcp.Output{Text: text, Structured: out}, nil
}

func hackEventTools() []mcp.Tool {
	tools := hackBaseEventTools()
	tools = append(tools, hackConnectionTools()...)
	tools = append(tools, hackMemberEventTools()...)
	tools = append(tools, hackOrganiserEventTools()...)
	return append(tools, hackWebEventTools()...)
}

func hackWebEventTools() []mcp.Tool {
	slug := map[string]any{"slug": mcp.Str("Event name.")}
	generic := map[string]any{"type": "object", "additionalProperties": true}
	return declare([]decl{
		{
			Name: "hack_set_event_icon", Title: "Set event icon",
			Description:  "Set the event icon from base64 PNG, JPEG or WebP bytes. The server checks actual image type and its configured size limit. Ask before replacing the icon. Organiser only.",
			InputSchema:  mcp.Object(map[string]any{"slug": slug["slug"], "content_type": map[string]any{"type": "string", "enum": []string{"image/png", "image/jpeg", "image/webp"}, "description": "The image's MIME type."}, "image_base64": mcp.Str("Base64-encoded image bytes, without a data: prefix.")}, "slug", "content_type", "image_base64"),
			OutputSchema: generic, Annotations: mcp.Writes(true, true, true),
			run: func(c *mcp.Call, args map[string]any) (mcp.Output, error) {
				path, err := hackWebPath(args, "/icon")
				if err != nil {
					return mcp.Output{}, err
				}
				typ, err := mcp.StringArg(args, "content_type")
				if err != nil {
					return mcp.Output{}, err
				}
				encoded, err := mcp.StringArg(args, "image_base64")
				if err != nil {
					return mcp.Output{}, err
				}
				if len(encoded) > base64.StdEncoding.EncodedLen(4<<20) {
					return mcp.Output{}, errors.New("image_base64 exceeds the maximum allowed image size")
				}
				body, err := base64.StdEncoding.DecodeString(encoded)
				if err != nil {
					return mcp.Output{}, errors.New("image_base64 must be valid base64 image bytes")
				}
				if typ != "image/png" && typ != "image/jpeg" && typ != "image/webp" {
					return mcp.Output{}, errors.New("content_type must be image/png, image/jpeg or image/webp")
				}
				return hackWebJSON(c, "hack_set_event_icon", http.MethodPut, path, body, map[string]string{"Content-Type": typ})
			},
		},
		{
			Name: "hack_clear_event_icon", Title: "Clear event icon",
			Description: "Remove the uploaded event icon and restore its generated initial icon. Ask the organiser first. Organiser only.",
			InputSchema: mcp.Object(slug, "slug"), OutputSchema: generic, Annotations: mcp.Writes(true, true, true),
			run: func(c *mcp.Call, args map[string]any) (mcp.Output, error) {
				path, err := hackWebPath(args, "/icon")
				if err != nil {
					return mcp.Output{}, err
				}
				return hackWebJSON(c, "hack_clear_event_icon", http.MethodDelete, path, nil, nil)
			},
		},
		{
			Name: "hack_get_public_event", Title: "Read public event data",
			Description: "Read the credentialless public event feed, including submissions_public and the submissions array (submitted, non-excluded entry summary cards when enabled from Judging onward), overall_prizes, eligible gallery, published results and closed vote ranking. No private codes or contact email are returned. Build an event website from hack_get_page_recipe topic event: it reads the public event feed live, so the page updates itself at every stage, results included; never copy stage, announcements or results into the HTML.",
			InputSchema: mcp.Object(slug, "slug"), OutputSchema: generic, Annotations: hackPublicReadOnly(),
			run: func(c *mcp.Call, args map[string]any) (mcp.Output, error) {
				path, err := hackWebPath(args, "/public")
				if err != nil {
					return mcp.Output{}, err
				}
				return hackWebJSON(c, "hack_get_public_event", http.MethodGet, path, nil, nil)
			},
		},
		{
			Name: "hack_get_preferences", Title: "Read my Simple Hack preferences",
			Description: "Read this account's theme and completed organiser/judge walkthrough flags.",
			InputSchema: mcp.NoArgs(), OutputSchema: generic, Annotations: mcp.ReadOnly(),
			run: func(c *mcp.Call, _ map[string]any) (mcp.Output, error) {
				return hackWebJSON(c, "hack_get_preferences", http.MethodGet, "/v1/hack/preferences", nil, nil)
			},
		},
		{
			Name: "hack_set_preferences", Title: "Set my Simple Hack preferences",
			Description:  "Set this account's theme (system, light or dark) and/or whether each walkthrough is complete. Sends only supplied fields.",
			InputSchema:  mcp.Object(map[string]any{"theme": map[string]any{"type": "string", "enum": []string{"system", "light", "dark"}, "description": "Colour theme for the person's Simple Hack pages: system follows their device."}, "organiser_walkthrough_done": map[string]any{"type": "boolean", "description": "true marks the organiser walkthrough as seen, so it stops showing."}, "judge_walkthrough_done": map[string]any{"type": "boolean", "description": "true marks the judge walkthrough as seen, so it stops showing."}}),
			OutputSchema: generic, Annotations: mcp.Writes(true, true, false),
			run: func(c *mcp.Call, args map[string]any) (mcp.Output, error) {
				body := map[string]any{}
				if theme, ok := args["theme"]; ok {
					body["theme"] = theme
				}
				if done, ok := args["organiser_walkthrough_done"]; ok {
					body["organiser_walkthrough_done"] = done
				}
				if done, ok := args["judge_walkthrough_done"]; ok {
					body["judge_walkthrough_done"] = done
				}
				if len(body) == 0 {
					return mcp.Output{}, errors.New("set at least one preference")
				}
				raw, err := json.Marshal(body)
				if err != nil {
					return mcp.Output{}, err
				}
				return hackWebJSON(c, "hack_set_preferences", http.MethodPatch, "/v1/hack/preferences", raw, nil)
			},
		},
	})
}

func hackEventSchemas() hackEventSchemaSet {
	nullableString := func(desc string) map[string]any {
		return map[string]any{"type": []any{"string", "null"}, "description": desc}
	}
	nullableInteger := func(desc string) map[string]any {
		return map[string]any{"type": []any{"integer", "null"}, "description": desc}
	}
	nullableObject := func(schema map[string]any, desc string) map[string]any {
		schema["type"] = []any{"object", "null"}
		schema["description"] = desc
		return schema
	}
	event := mcp.OutObject(map[string]any{
		"slug":               mcp.OutString("The event name."),
		"title":              mcp.OutString("The event title."),
		"tagline":            mcp.OutString("Short line under the title. Empty when unset."),
		"about":              mcp.OutString("About text. Empty when unset."),
		"rules":              mcp.OutString("Rules text. Empty when unset."),
		"prizes":             mcp.OutString("Prizes text. Empty when unset."),
		"submissions_public": map[string]any{"type": "boolean", "description": "Whether submitted entries are public from Judging onward."},
		"overall_prizes":     map[string]any{"type": "array", "items": mcp.OutString("Prize for this place."), "description": "Prizes for 1st, 2nd, 3rd by index. Always an array."},
		"coc_text":           mcp.OutString("The organiser's own code of conduct. Empty when unset."),
		"theme":              mcp.OutString("Page style for the public event page: poster, confetti, boardroom, garden, terminal, or empty for the classic look. The organiser's accent_color still drives buttons and links under every style."),
		"stage":              mcp.OutString("draft, open, building, closed, judging, results or archived."),
		"can":                hackAbilitiesSchema(),
		"role":               mcp.OutString("This person's role: organiser, participant or judge."),
		"url":                mcp.OutString("The event's public address."),
		"time_zone":          mcp.OutString("IANA time zone."),
		"team_size_max":      mcp.OutInteger("Maximum people on a team."),
		"team_finding":       mcp.OutBool("Whether private team finding is enabled."),
		"taken_down":         mcp.OutBool("Whether the operator has taken the event down."),
		"stages_offered":     mcp.OutArray("Stages hack_set_event_stage accepts.", mcp.OutString("A stage name.")),
		"starts_at":          nullableString("Start time, or null when unset."),
		"ends_at":            nullableString("End time, or null when unset."),
		"judging_ends_at":    nullableString("When judging closes, or null when unset. Judges with unscored teams are reminded before this time, once set."),
		"checklist":          nullableObject(hackChecklistSchema(), "Role-specific checklist and guided next action."),
		"next_step": mcp.WithDescription(mcp.OutObject(map[string]any{
			"kind":            mcp.OutString("stage, entry_rules, publish_results or none. entry_rules means a draft event whose organiser hasn't chosen entry rules yet: call hack_set_entry_rules with the organiser's choice of preset first (ask which: video_or_website or video_website_code), then this becomes stage/open (\"Open sign-up\")."),
			"stage":           mcp.OutString("The stage hack_set_event_stage would move to. Empty for entry_rules, publish_results or none."),
			"label":           mcp.OutString("The button's own text, e.g. \"Start building\"."),
			"deadline_passed": mcp.OutBool("True only for Close entries once the submission deadline has passed."),
			"blocked":         nullableString("archive_needs_participants (End with nobody in the event), or null."),
			"cue": nullableObject(mcp.OutObject(map[string]any{
				"key":      mcp.OutString("The fixed row this comes from (entry_deadline, event_start, event_end), or empty for a row the organiser typed into the schedule."),
				"title":    mcp.OutString("The schedule row's title."),
				"start_at": mcp.OutString("When the row starts, RFC 3339."),
				"end_at":   nullableString("When the row ends, or null for a moment such as a deadline."),
				"fixed":    mcp.OutBool("True when the row comes from the event's settings and can't be edited in the schedule."),
				"now":      mcp.OutBool("True while the row is on (or within 15 minutes of a moment)."),
				"text":     mcp.OutString("The website's wording, e.g. \"Kickoff is now. Open sign-up?\". Ask the organiser. Nothing moves until they agree."),
			}, "key", "title", "start_at", "end_at", "fixed", "now", "text"), "The schedule row that lines up with this move once its time has come, or null. It only prompts: the stage never moves by itself."),
		}, "kind", "stage", "label", "deadline_passed", "blocked", "cue"), "The one forward move the website offers (its one button) for the current stage. Present only when this person is the organiser."),
		"organiser": mcp.WithDescription(mcp.OutObject(map[string]any{
			"organiser_name":        mcp.OutString("Public event organiser name. Membership uses the account name; if empty, this fills it once."),
			"organisation":          mcp.OutString("Organisation or community. Empty when unset."),
			"contact_email":         mcp.OutString("Contact email."),
			"purpose":               mcp.OutString("What the event is for."),
			"expected_participants": nullableInteger("How many participants are expected, or null."),
			"join_code":             mcp.OutString("Code in the join link."),
			"join_url":              mcp.OutString("Link participants use to join. Give this to the person exactly."),
			"judge_code":            mcp.OutString("Code in the judge link."),
			"judge_url":             mcp.OutString("Link judges use to join. Give this to the person exactly."),
			"counts": mcp.OutObject(map[string]any{
				"participants": mcp.OutInteger("Participants, not counting organisers or judges."),
				"teams":        mcp.OutInteger("Teams."),
				"judges":       mcp.OutInteger("Judges."),
				"on_no_team":   mcp.OutInteger("Participants who have not joined a team."),
			}, "participants", "teams", "judges", "on_no_team"),
		}, "organiser_name", "organisation", "contact_email", "purpose", "expected_participants", "join_code", "join_url", "judge_code", "judge_url", "counts"), "Present only when this person is the organiser."),
	}, "slug", "title", "tagline", "about", "rules", "prizes", "overall_prizes", "submissions_public", "coc_text", "theme", "stage", "role", "url", "time_zone", "team_size_max", "taken_down", "stages_offered", "starts_at", "ends_at", "judging_ends_at")
	listItem := mcp.OutObject(map[string]any{
		"slug":       mcp.OutString("The event name."),
		"title":      mcp.OutString("The event title."),
		"stage":      mcp.OutString("The event stage."),
		"can":        hackAbilitiesSchema(),
		"ended_at":   nullableString("When the event was ended."),
		"role":       mcp.OutString("This person's role on the event."),
		"url":        mcp.OutString("The event's public address."),
		"manage_url": mcp.OutString("The organiser's page for the event."),
		"time_zone":  mcp.OutString("IANA time zone."),
		"taken_down": mcp.OutBool("Whether the operator has taken the event down."),
		"starts_at":  nullableString("Start time, or null when unset."),
		"ends_at":    nullableString("End time, or null when unset."),
	}, "slug", "title", "stage", "role", "url", "manage_url", "time_zone", "taken_down", "starts_at", "ends_at")
	name := mcp.OutObject(map[string]any{
		"slug":      mcp.OutString("The name that was checked."),
		"available": mcp.OutBool("True when the name can be used for a new event."),
		"address":   mcp.OutString("The address the event would have."),
		"code":      mcp.OutString("Why the name is unavailable, when available is false."),
		"error":     mcp.OutString("The same reason in a sentence, when available is false."),
	}, "slug", "available", "address")
	return hackEventSchemaSet{list: mcp.OutObject(map[string]any{"events": mcp.OutArray("Events this person is part of.", listItem), "count": mcp.OutInteger("How many events.")}, "events", "count"), name: name, event: event}
}

type hackEventSchemaSet struct{ list, name, event map[string]any }

func hackAbilitiesSchema() map[string]any {
	return mcp.OutObject(map[string]any{"manage": mcp.OutBool("May manage the event."), "judge": mcp.OutBool("May enter judging."), "take_part": mcp.OutBool("May enter the participant area.")}, "manage", "judge", "take_part")
}
