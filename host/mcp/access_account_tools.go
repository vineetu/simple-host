package mcp

// Account family: identity and showcase profile.

import (
	"cmp"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

func accountTools() []Tool {
	return []Tool{
		{
			policy: toolPolicy{analytics: analyticsPolicy{structuredText: true, emailIdentity: true}},
			OutputSchema: outObject(map[string]any{
				"can_set_keep_versions": outBool("Whether the operator allows this account to set version counts."),
				"keep_versions":         outInteger("Account default version count; 0 is unlimited."),
				"file_usage":            outObject(map[string]any{"used_bytes": outInteger("Live files and all kept versions, including Recently deleted; backend resources excluded."), "limit_mb": outInteger("0 is unlimited."), "message": outString("Plain account usage."), "tips": outString("Ways to reduce files.")}, "used_bytes", "limit_mb", "message", "tips"),
				"home_site":             map[string]any{"type": []string{"string", "null"}, "description": "Selected home site; null means showcase."},
				"email":                 outString("Empty unless include_email is true, when it is the email address the account signs in with."),
				"handle":                outString("The account's handle: the <handle> in its page https://<handle>.simple-host.app/ and in every site address https://<site>.<handle>.simple-host.app/. Absent until the account publishes its first site."),
				"public_page":           outString("Address of the account's public page listing its sites. Present with handle."),
				"display_name":          outString("Omitted from connector results."),
				"address": withDescription(outObject(map[string]any{
					"state":          outEnum("ready (sites are at https://<site>.<handle>.simple-host.app/), waiting (its certificate is queued) or failing (retried automatically).", "ready", "waiting", "failing"),
					"address":        outString("The pattern of the account's own site addresses."),
					"ready_in_hours": outInteger("Rough hours until the own address is ready. Present while waiting or failing."),
					"note":           outString("Tell the person this while waiting or failing: where their sites are until then, and that visitors' sign-ins and browser-kept data start fresh when the address switches."),
				}, "state", "address"), "The account's own site address. Present with handle on simple-host.app."),
				"teams": outArray("Present when you belong to teams: each one's id (it never changes; record it rather than the handle) and handle (owner=<handle> on site tools).", outObject(map[string]any{
					"id":     outString("The team's id."),
					"handle": outString("The team's handle."),
				}, "id", "handle")),
				"team_invitations": outArray("Present when invitations to join a team wait for your answer: accept or decline each with answer_team_invitation, after asking the person.", invitationSchema(false)),
			}, "email"),
			Name:        "who_am_i",
			Title:       "Who am I signed in as",
			Description: "Return the Simple Host account this connection acts as: its handle (the <handle> in its page https://<handle>.simple-host.app/ and its site addresses https://<site>.<handle>.simple-host.app/) and its public page listing its sites. Email is returned only when include_email is true.",
			InputSchema: object(map[string]any{"include_email": map[string]any{"type": "boolean", "description": "Set true only when the person asks which email they are signed in with."}}),
			Annotations: readOnly(),
			run:         runWhoAmI,
		},
		{
			policy:       toolPolicy{groupSiteExcluded: true},
			OutputSchema: outObject(map[string]any{"bio": outString("Public plain-text bio."), "max_length": outInteger("Configured maximum characters.")}, "bio", "max_length"),
			Name:         "set_bio", Title: "Set my showcase bio",
			Description: "Set the short plain-text bio shown on the person's public page (https://<handle>.simple-host.app/, the showcase of their sites). An empty string clears it; the limit is about 280 characters. " +
				"It replaces the current bio, which is not kept anywhere, so ask the person before replacing or clearing a bio they wrote (their public page shows the current one).",
			InputSchema: object(map[string]any{"bio": str("Plain text, or empty to clear the bio.")}, "bio"),
			// Overwrites the earlier bio with no copy kept: hard to reverse.
			Annotations: writes(true, true, true),
			run:         runSetBio,
		},
		{
			policy:       toolPolicy{groupSiteExcluded: true},
			OutputSchema: outObject(map[string]any{"site": outString("Owned site."), "pinned": outBool("Pinned to the first group."), "order": outInteger("Smaller numbers come first.")}, "site", "pinned", "order"),
			Name:         "set_showcase_site", Title: "Pin or order a showcase site",
			Description: "Pin a site to the top of the person's public page (the showcase of their sites), or set its position. Pinned sites come first, then smaller order numbers. Sites with List on my profile off, offline sites, and passcode-protected sites never appear there, pinned or not. Supply pinned, order, or both.",
			InputSchema: object(map[string]any{"site": str(siteDesc), "pinned": map[string]any{"type": "boolean", "description": "true pins the site to the top of the showcase; false unpins it."}, "order": map[string]any{"type": "integer", "minimum": 0, "maximum": 1000000, "description": "Manual position among the pinned or unpinned sites: smaller numbers come first."}}, "site"),
			Annotations: writes(true, true, true),
			run:         runSetShowcaseSite,
		},
	}
}

// restMe is GET /v1/me.
type restMe struct {
	Username           string              `json:"username"`
	Handle             string              `json:"handle"`
	HomeSite           *string             `json:"home_site"`
	FileUsage          map[string]any      `json:"file_usage"`
	CanSetKeepVersions bool                `json:"can_set_keep_versions"`
	KeepVersions       int                 `json:"keep_versions"`
	PublicPage         string              `json:"public_page"`
	Teams              []map[string]string `json:"teams"`
	TeamInvitations    []map[string]any    `json:"team_invitations"`
	Address            *struct {
		State        string `json:"state"`
		Address      string `json:"address"`
		ReadyInHours *int   `json:"ready_in_hours"`
		Note         string `json:"note"`
	} `json:"address"`
}

func runWhoAmI(c *call, args map[string]any) (output, error) {
	var me restMe
	if err := c.doJSON("who_am_i", http.MethodGet, "/v1/me", nil, &me); err != nil {
		return output{}, err
	}
	include, err := boolArg(args, "include_email", false)
	if err != nil {
		return output{}, err
	}
	out := map[string]any{"email": "", "home_site": me.HomeSite, "can_set_keep_versions": me.CanSetKeepVersions, "keep_versions": me.KeepVersions}
	text := "Signed in to Simple Host."
	if include {
		out["email"] = me.Username
		text = "Signed in to Simple Host as " + me.Username + "."
	}
	if me.FileUsage != nil {
		out["file_usage"] = me.FileUsage
		text += " " + fmt.Sprint(me.FileUsage["message"]) + "."
	}
	text += me.handleText(out, c.server.cfg.ContentOrigin) + me.teamsText(out) + me.addressText(out)
	return output{Text: text, Structured: out}, nil
}

func (me restMe) handleText(out map[string]any, contentOrigin string) string {
	if me.Handle == "" {
		return ""
	}
	page := cmp.Or(me.PublicPage, contentOrigin+"/"+me.Handle)
	out["handle"], out["public_page"] = me.Handle, page
	return " Handle: " + me.Handle + ". Public page: " + page
}

func (me restMe) teamsText(out map[string]any) string {
	text := ""
	if len(me.Teams) > 0 {
		teams := make([]any, 0, len(me.Teams))
		handles := make([]string, 0, len(me.Teams))
		for _, t := range me.Teams {
			teams = append(teams, map[string]any{"id": t["id"], "handle": t["handle"]})
			handles = append(handles, t["handle"])
		}
		out["teams"] = teams
		text += " Teams: " + strings.Join(handles, ", ") + " (pass owner=<team> on site tools to work on a team's sites)."
	}
	if len(me.TeamInvitations) > 0 {
		invitations := make([]any, 0, len(me.TeamInvitations))
		teams := make([]string, 0, len(me.TeamInvitations))
		for _, v := range me.TeamInvitations {
			invitations = append(invitations, v)
			teams = append(teams, fmt.Sprint(v["team"]))
		}
		out["team_invitations"] = invitations
		text += " Invitations to join: " + strings.Join(teams, ", ") + " (ask the person, then answer_team_invitation)."
	}
	return text
}

func (me restMe) addressText(out map[string]any) string {
	a := me.Address
	if a == nil || a.State == "" {
		return ""
	}
	addr := map[string]any{"state": a.State, "address": a.Address}
	if a.ReadyInHours != nil {
		addr["ready_in_hours"] = *a.ReadyInHours
	}
	out["address"] = addr
	if a.Note == "" {
		return ""
	}
	addr["note"] = a.Note
	return " " + a.Note
}

func runSetBio(c *call, args map[string]any) (output, error) {
	bio, ok := args["bio"].(string)
	if !ok {
		return output{}, fmt.Errorf("bio must be a string")
	}
	body, _ := json.Marshal(map[string]any{"bio": bio})
	var result map[string]any
	if err := c.doJSON("set_bio", http.MethodPut, "/v1/me/bio", body, &result); err != nil {
		return output{}, err
	}
	return output{Text: "Showcase bio updated.", Structured: result}, nil
}

func runSetShowcaseSite(c *call, args map[string]any) (output, error) {
	name, err := siteArg(args)
	if err != nil {
		return output{}, err
	}
	payload := map[string]any{}
	if v, ok := args["pinned"]; ok {
		payload["pinned"] = v
	}
	if v, ok := args["order"]; ok {
		payload["order"] = v
	}
	if len(payload) == 0 {
		return output{}, fmt.Errorf("supply pinned and/or order")
	}
	body, _ := json.Marshal(payload)
	var result map[string]any
	if err := c.doJSON("set_showcase_site", http.MethodPut, "/v1/sites/"+url.PathEscape(name)+"/showcase", body, &result); err != nil {
		return output{}, err
	}
	return output{Text: "Showcase pin and order updated.", Structured: result}, nil
}
