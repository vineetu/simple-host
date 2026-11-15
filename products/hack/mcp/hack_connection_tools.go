// famHackEvents connection selection.
package hackmcp

import (
	"errors"

	"github.com/vsriram/simple-host/internal/mcp"
)

// hackConnectionTools controls the selected site for a hosted personal OAuth
// connection. Event actions continue to use the person's own identity.
func hackConnectionTools() []mcp.Tool {
	return declare([]decl{{
		Name:        "hack_select_team",
		Title:       "Choose my team's site",
		Description: "Choose or switch the team site this connection can publish. First call hack_get_my_teams to find your current team_id. The server checks your current approved participant membership now and on every later site call; choosing one team never grants access to another. Ask the person before publishing a new site.",
		InputSchema: mcp.Object(map[string]any{
			"team_id": mcp.Str("The UUID of your current event team to publish for."),
		}, "team_id"),
		OutputSchema: mcp.OutObject(map[string]any{
			"team_id": mcp.OutString("The selected team ID. Future site tools use this team's scoped credential."),
		}, "team_id"),
		Annotations: mcp.Writes(true, true, false),
		run: func(c *mcp.Call, args map[string]any) (mcp.Output, error) {
			teamID, err := mcp.StringArg(args, "team_id")
			if err != nil {
				return mcp.Output{}, err
			}
			if c.Caller().GrantID == "" || c.Caller().UserID == "" || !c.CanSelectTarget() {
				return mcp.Output{}, errors.New("hack_select_team requires a signed-in personal Simple Hack connector connection")
			}
			if err := c.SelectTarget(teamID); err != nil {
				return mcp.Output{}, err
			}
			return mcp.Output{Text: "Selected team " + teamID + ". Future site tools are scoped to this team.", Structured: map[string]any{"team_id": teamID}}, nil
		},
	}})
}
