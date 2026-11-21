package hackmcp

import (
	"encoding/json"
	"github.com/vsriram/simple-host/host/mcp"
	"net/http"
)

// hackWhoAmITool is who_am_i on Simple Hack. A person owns no sites there, so it
// names the signed-in person, never a site address (the team site is published
// with owner set to the event).
func hackWhoAmITool() mcp.Tool {
	return decl{
		Name:        "who_am_i",
		Title:       "Who am I signed in as",
		Description: "Return who this Simple Hack connection acts as. Email is returned only when include_email is true. hack_list_events lists the person's events and roles; hack_get_my_teams their teams and team sites.",
		InputSchema: mcp.Object(map[string]any{"include_email": map[string]any{"type": "boolean", "description": "Set true only when the person asks which email they are signed in with."}}),
		Annotations: mcp.ReadOnly(),
		OutputSchema: mcp.OutObject(map[string]any{
			"email": mcp.OutString("The email address the person signs in with. Absent unless include_email is true."),
		}),
		run: runHackWhoAmI,
	}.tool()
}

const hackWhoAmINote = ". Simple Hack accounts have no personal sites; websites belong to teams and events. hack_list_events lists your events and your role in each; to publish your team's site, call the site and storage tools with site set to your team's site and owner set to the event's name (hack_get_my_teams lists both)."

func runHackWhoAmI(c *mcp.Call, args map[string]any) (mcp.Output, error) {
	includeEmail, err := mcp.BoolArg(args, "include_email", false)
	if err != nil {
		return mcp.Output{}, err
	}
	res := c.Do(http.MethodGet, "/v1/me", nil, nil)
	if !res.OK() {
		return mcp.Output{}, mcp.RestError("who_am_i", res)
	}
	var me struct {
		Username string `json:"username"`
	}
	_ = json.Unmarshal(res.Body(), &me)
	out := map[string]any{}
	text := "Signed in to Simple Hack"
	if includeEmail && me.Username != "" {
		out["email"] = me.Username
		text += " as " + me.Username
	}
	return mcp.Output{Text: text + hackWhoAmINote, Structured: out}, nil
}
