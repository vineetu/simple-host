package mcp

import "errors"

// HackConnectionTools controls the selected site for a hosted personal OAuth
// connection. Event actions continue to use the person's own identity.
func HackConnectionTools() []Tool {
	return []Tool{{
		Name:        "hack_select_team",
		Title:       "Choose my team's site",
		Description: "Choose or switch the team site this connection can publish. First call hack_get_my_teams to find your current team_id. The server checks your current approved participant membership now and on every later site call; choosing one team never grants access to another. Ask the person before publishing a new site.",
		InputSchema: object(map[string]any{
			"team_id": str("The UUID of your current event team to publish for."),
		}, "team_id"),
		OutputSchema: outObject(map[string]any{
			"team_id": outString("The selected team ID. Future site tools use this team's scoped credential."),
		}, "team_id"),
		Annotations: writes(false, true, false),
		run: func(c *call, args map[string]any) (output, error) {
			teamID, err := stringArg(args, "team_id")
			if err != nil {
				return output{}, err
			}
			if c.caller.GrantID == "" || c.caller.UserID == "" || c.server.cfg.SelectHackTeam == nil {
				return output{}, errors.New("hack_select_team requires a signed-in personal Simple Hack connector connection")
			}
			if err := c.server.cfg.SelectHackTeam(c.orig.Context(), c.caller, teamID); err != nil {
				return output{}, err
			}
			return output{Text: "Selected team " + teamID + ". Future site tools are scoped to this team.", Structured: map[string]any{"team_id": teamID}}, nil
		},
	}}
}
