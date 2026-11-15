// famHackEvents supplies the Hack identity tool used by personal connections.
package hackmcp

import (
	"encoding/json"
	"github.com/vsriram/simple-host/internal/mcp"
	"net/http"
)

// hackWhoAmITool is who_am_i on Simple Hack. A person there owns no sites of
// their own, so it names the signed-in person and the team the website tools
// act for, never a personal site address. On a personal connection it runs
// before a team is selected (callTool lets it through the team gate).
func hackWhoAmITool() mcp.Tool {
	return decl{
		Name:        "who_am_i",
		Title:       "Who am I signed in as",
		Description: "Return who this Simple Hack connection acts as: when one is selected, the team whose website the site and storage tools change. Works before a team is selected. Email is returned only when include_email is true. hack_list_events lists the person's events and roles.",
		InputSchema: mcp.Object(map[string]any{"include_email": map[string]any{"type": "boolean", "description": "Set true only when the person asks which email they are signed in with."}}),
		Annotations: mcp.ReadOnly(),
		OutputSchema: mcp.OutObject(map[string]any{
			"email": mcp.OutString("The email address the person signs in with. Absent unless include_email is true, and always absent on a team key connection."),
			"team": mcp.WithDescription(mcp.OutObject(map[string]any{
				"event": mcp.OutString("The event's name in its address."),
				"team":  mcp.OutString("The team's name in its address."),
				"site":  mcp.OutString("The team website's site name, used as `site` in the site and storage tools."),
			}, "event", "team", "site"), "The team the site and storage tools act for. Absent when none is selected."),
		}),
		run: func(c *mcp.Call, args map[string]any) (mcp.Output, error) {
			out := map[string]any{}
			text := ""
			team, err := hackMe(c, c.Caller().APIKey, out)
			if err != nil {
				return mcp.Output{}, err
			}
			if email, ok := out["email"].(string); ok {
				text = "Signed in to Simple Hack"
				if include, _ := args["include_email"].(bool); include {
					text += " as " + email
				} else {
					delete(out, "email")
				}
				text += ". Simple Hack accounts have no personal sites; websites belong to teams and events. hack_list_events lists your events and your role in each."
			}
			if team == nil && c.Caller().TeamAPIKey != "" {
				if team, err = hackMe(c, c.Caller().TeamAPIKey, nil); err != nil {
					return mcp.Output{}, err
				}
			}
			if team != nil {
				out["team"] = team
				if text != "" {
					text += " "
				}
				text += "The site and storage tools act for team " + team["team"] + " in event " + team["event"] + "; its site is " + team["site"] + "."
			} else if c.Caller().Mode == mcp.CallerModeEvents {
				if text != "" {
					text += " "
				}
				text += "No team is selected, so the site and storage tools are not available yet; choose one with hack_select_team."
			}
			return mcp.Output{Text: text, Structured: out}, nil
		},
	}.tool()
}

// hackMe reads GET /v1/me with key. A team credential answers with its team,
// which is returned; a person's email goes into out when out is not nil.
func hackMe(c *mcp.Call, key string, out map[string]any) (map[string]string, error) {
	caller := c.Caller()
	caller.APIKey = key
	res := c.WithCaller(caller).Do(http.MethodGet, "/v1/me", nil, nil)
	if !res.OK() {
		return nil, mcp.RestError("who_am_i", res)
	}
	var me struct {
		Username string `json:"username"`
		Team     *struct {
			Event string `json:"event"`
			Team  string `json:"team"`
			Site  string `json:"site"`
		} `json:"team"`
	}
	_ = json.Unmarshal(res.Body(), &me)
	if out != nil && me.Username != "" {
		out["email"] = me.Username
	}
	if me.Team == nil {
		return nil, nil
	}
	return map[string]string{"event": me.Team.Event, "team": me.Team.Team, "site": me.Team.Site}, nil
}
