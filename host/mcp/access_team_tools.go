package mcp

// Teams family: a team is an account that owns
// sites and that people join. One role: any member manages the team's sites
// (every site and storage tool takes owner=<team>), invites people and
// removes members, and deletes it once it owns no live sites. Nobody joins
// without accepting an invitation. Registered only where Config.Teams is set.

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"strings"
)

const teamArgDesc = "The team's handle, as list_teams shows it (the <team> in https://<site>.<team>.simple-host.app/)."

// ownerArgDesc is the owner argument every site and storage tool takes.
const ownerArgDesc = "For a team's site, the team's handle (list_teams); for a site you edit for someone, their handle. Leave out for your own sites."

func teamSchema() map[string]any {
	return outObject(map[string]any{
		"id":         outString("The team's id. It never changes: record it rather than the handle."),
		"handle":     outString("The team's handle: owner=<handle> on site tools, and the <team> in its sites' addresses."),
		"members":    outInteger("How many people belong to the team."),
		"sites":      outInteger("Live sites the team owns."),
		"created_at": outString("When the team was made (RFC3339)."),
	}, "id", "handle", "members", "created_at")
}

func memberSchema() map[string]any {
	return outObject(map[string]any{
		"id":           outString("The person's account id."),
		"handle":       outString("The person's handle (their address). Empty until they publish a site."),
		"display_name": outString("Their name, when set."),
		"added_by":     outString("Handle of the member who added them."),
		"added_at":     outString("When they joined (RFC3339)."),
	}, "id")
}

func invitationSchema(forMembers bool) map[string]any {
	props := map[string]any{
		"id":         outString("The invitation's id."),
		"team":       outString("The team's handle."),
		"invited_by": outString("Handle of the member who sent it."),
		"created_at": outString("When it was sent (RFC3339)."),
		"expires_at": outString("When it lapses unanswered (RFC3339)."),
	}
	if forMembers {
		props["addressed_to"] = outString("The email it was sent to.")
	}
	return outObject(props, "id", "team")
}

func teamPath(args map[string]any, suffix string) (string, error) {
	team, err := stringArg(args, "team")
	if err != nil {
		return "", err
	}
	return "/v1/teams/" + url.PathEscape(strings.ToLower(team)) + suffix, nil
}

func teamCall(c *call, tool, method, path string, body any) (map[string]any, error) {
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	res := c.do(method, path, raw, nil)
	if !res.ok() {
		return nil, restError(tool, res)
	}
	var out map[string]any
	if err := json.Unmarshal(res.body, &out); err != nil {
		return nil, errors.New(tool + ": unexpected answer from the server")
	}
	return out, nil
}

func teamTools() []Tool {
	policy := toolPolicy{notDeployOnly: true}
	return []Tool{
		{
			policy:       policy,
			OutputSchema: teamSchema(),
			Name:         "create_team", Title: "Create a team",
			Description: "Create a team: an account that owns sites, which people join. You become its first member. Its name becomes its address, https://<site>.<name>.simple-host.app/, so it follows the address rules: lowercase letters, numbers and hyphens, no dots. A team cannot be renamed later. Ask the person for the name first.",
			InputSchema: object(map[string]any{"name": str("The team's name and address: 1 to 39 lowercase letters, numbers or hyphens.")}, "name"),
			Annotations: writes(false, false, false),
			run:         runCreateTeam,
		},
		{
			policy: policy,
			OutputSchema: outObject(map[string]any{
				"teams":       outArray("The teams you belong to.", teamSchema()),
				"invitations": outArray("Invitations to join a team that wait for your answer (answer_team_invitation).", invitationSchema(false)),
			}, "teams"),
			Name: "list_teams", Title: "List my teams",
			Description: "List the teams you belong to, with each team's id and handle, and the invitations to join a team that wait for your answer. Pass a team's handle as owner on site and storage tools to work on its sites.",
			InputSchema: noArgs(),
			Annotations: readOnly(),
			run:         runListTeams,
		},
		{
			policy: policy,
			OutputSchema: outObject(map[string]any{
				"team":        teamSchema(),
				"members":     outArray("The team's members, earliest first. Emails are never shown.", memberSchema()),
				"invitations": outArray("The team's invitations still waiting for an answer.", invitationSchema(true)),
			}, "team", "members"),
			Name: "list_team_members", Title: "List a team's members",
			Description: "List the members of a team you belong to (each one's handle, name, who added them and when) and its invitations still waiting for an answer.",
			InputSchema: object(map[string]any{"team": str(teamArgDesc)}, "team"),
			Annotations: readOnly(),
			run:         runListTeamMembers,
		},
		{
			policy: policy,
			OutputSchema: outObject(map[string]any{
				"invited":         outBool("Always true: the answer is the same whether or not anyone has that email."),
				"expires_in_days": outInteger("Days the invitation waits for an answer."),
				"note":            outString("What happens next."),
			}, "invited"),
			Name: "add_team_member", Title: "Invite a person to a team",
			Description: "Invite a person to a team you belong to, by their email. Nobody becomes a member until they accept: they see the invitation when signed in with that email (who_am_i or list_teams) and accept or decline it. Nothing is sent to them, so tell them yourself. The answer is the same whether or not anyone has that email. Every member can publish, change and delete all the team's sites and invite or remove members, so confirm the person and the team with the person you are helping first. Invitations lapse after 14 days; a team may have 20 waiting.",
			InputSchema: object(map[string]any{"team": str(teamArgDesc), "email": str("The person's sign-in email.")}, "team", "email"),
			Annotations: writes(false, false, false),
			run:         runAddTeamMember,
		},
		{
			policy: policy,
			OutputSchema: outObject(map[string]any{
				"cancelled": outString("The invitation's id."),
			}, "cancelled"),
			Name: "cancel_team_invitation", Title: "Cancel a team invitation",
			Description: "Cancel an invitation of a team you belong to that is still waiting for an answer (its id from list_team_members).",
			InputSchema: object(map[string]any{"team": str(teamArgDesc), "invitation": str("The invitation's id, from list_team_members.")}, "team", "invitation"),
			Annotations: writes(true, true, false),
			run:         runCancelTeamInvitation,
		},
		{
			policy: policy,
			OutputSchema: outObject(map[string]any{
				"team":   outString("The team's handle."),
				"answer": outString("accept or decline."),
			}, "team", "answer"),
			Name: "answer_team_invitation", Title: "Accept or decline a team invitation",
			Description: "Accept or decline an invitation to join a team that waits for you (who_am_i or list_teams lists them). Accepting makes you a member: you can then publish, change and delete all the team's sites with owner=<team>, as every member can. Ask the person which answer they want first.",
			InputSchema: object(map[string]any{
				"invitation": str("The invitation's id."),
				"answer":     map[string]any{"type": "string", "enum": []string{"accept", "decline"}, "description": "accept or decline."},
			}, "invitation", "answer"),
			// Declining removes the invitation for good.
			Annotations: writes(true, false, false),
			run:         runAnswerTeamInvitation,
		},
		{
			policy: policy,
			OutputSchema: outObject(map[string]any{
				"removed": outString("The handle or id that was removed."),
			}, "removed"),
			Name: "remove_team_member", Title: "Remove a person from a team",
			Description: "Remove a member from a team you belong to (yourself included), by handle or account id. Their access to the team's sites stops on their next request, and invitations they sent are withdrawn. The last member cannot leave: to end the team, delete its sites, then the team. Ask before removing anyone.",
			InputSchema: object(map[string]any{"team": str(teamArgDesc), "person": str("The member's handle or account id, from list_team_members.")}, "team", "person"),
			Annotations: writes(true, true, false),
			run:         runRemoveTeamMember,
		},
		{
			policy: policy,
			OutputSchema: outObject(map[string]any{
				"deleted": outString("The team's handle."),
			}, "deleted"),
			Name: "delete_team", Title: "Delete a team",
			Description: "Delete a team you belong to. Only a team with no live sites can be deleted: delete its sites first; any in Recently deleted are deleted for good with it. Its name is retired and never given out again. Ask the person before deleting.",
			InputSchema: object(map[string]any{"team": str(teamArgDesc)}, "team"),
			Annotations: writes(true, true, false),
			run:         runDeleteTeam,
		},
	}
}

// ownerTools adds owner to every site and storage tool that names a site (and
// to list_sites and list_deleted_sites), and expected_version to update_site
// and rollback_site, for a program that offers teams.
func ownerTools(tools []Tool) []Tool {
	for i := range tools {
		props, _ := tools[i].InputSchema["properties"].(map[string]any)
		_, site := props["site"]
		name := tools[i].Name
		if !site && name != "list_sites" && name != "list_deleted_sites" {
			continue
		}
		copied := make(map[string]any, len(props)+2)
		for k, v := range props {
			copied[k] = v
		}
		copied["owner"] = str(ownerArgDesc)
		if name == "update_site" || name == "rollback_site" {
			copied["expected_version"] = map[string]any{"type": "integer", "minimum": 1,
				"description": "The active version you read (get_site or list_sites). On a team's site send it: the change is refused with version_changed when another member published since, so nobody's work is overwritten unknowingly."}
		}
		schema := make(map[string]any, len(tools[i].InputSchema))
		for k, v := range tools[i].InputSchema {
			schema[k] = v
		}
		schema["properties"] = copied
		tools[i].InputSchema = schema
	}
	return tools
}

// withOwnerArg gives a tool's owner argument another description.
func withOwnerArg(tool Tool, desc string) Tool {
	props, _ := tool.InputSchema["properties"].(map[string]any)
	if _, ok := props["owner"]; !ok {
		return tool
	}
	copied := maps.Clone(props)
	copied["owner"] = str(desc)
	tool.InputSchema = maps.Clone(tool.InputSchema)
	tool.InputSchema["properties"] = copied
	return tool
}

// takeTeamArguments moves owner and expected_version from a tool's arguments
// onto the call. The tool's schema already allowed them (unexpectedArgument).
func (c *call) takeTeamArguments(args map[string]any) error {
	if _, present := args["owner"]; present {
		owner, err := optionalString(args, "owner")
		if err != nil {
			return err
		}
		delete(args, "owner")
		if strings.ContainsAny(owner, "/?#&") {
			return codedError("invalid_argument", "owner must be an account's handle, e.g. design-team")
		}
		c.owner = strings.ToLower(owner)
	}
	if _, present := args["expected_version"]; present {
		n, _, err := wholeNumber(args, "expected_version", false, 1, 1<<30)
		if err != nil {
			return err
		}
		delete(args, "expected_version")
		c.ifMatch = n
	}
	return nil
}

func runCreateTeam(c *call, args map[string]any) (output, error) {
	name, err := stringArg(args, "name")
	if err != nil {
		return output{}, err
	}
	out, err := teamCall(c, "create_team", http.MethodPost, "/v1/teams", map[string]string{"name": name})
	if err != nil {
		return output{}, err
	}
	return output{Text: fmt.Sprintf("Team %v created. Publish its sites with owner: %q on create_site and the other site tools; invite people with add_team_member.", out["handle"], out["handle"]), Structured: out}, nil
}

func runListTeams(c *call, args map[string]any) (output, error) {
	out, err := teamCall(c, "list_teams", http.MethodGet, "/v1/teams", nil)
	if err != nil {
		return output{}, err
	}
	return output{Text: jsonText(out), Structured: out}, nil
}

func runListTeamMembers(c *call, args map[string]any) (output, error) {
	path, err := teamPath(args, "/members")
	if err != nil {
		return output{}, err
	}
	out, err := teamCall(c, "list_team_members", http.MethodGet, path, nil)
	if err != nil {
		return output{}, err
	}
	return output{Text: jsonText(out), Structured: out}, nil
}

func runAddTeamMember(c *call, args map[string]any) (output, error) {
	path, err := teamPath(args, "/members")
	if err != nil {
		return output{}, err
	}
	email, err := stringArg(args, "email")
	if err != nil {
		return output{}, err
	}
	out, err := teamCall(c, "add_team_member", http.MethodPost, path, map[string]string{"email": email})
	if err != nil {
		return output{}, err
	}
	return output{Text: "Invitation recorded. They join only once they accept it, signed in with that email; nothing is sent to them, so let them know.", Structured: out}, nil
}

func runCancelTeamInvitation(c *call, args map[string]any) (output, error) {
	id, err := stringArg(args, "invitation")
	if err != nil {
		return output{}, err
	}
	path, err := teamPath(args, "/invitations/"+url.PathEscape(id))
	if err != nil {
		return output{}, err
	}
	out, err := teamCall(c, "cancel_team_invitation", http.MethodDelete, path, nil)
	if err != nil {
		return output{}, err
	}
	return output{Text: "Invitation cancelled.", Structured: out}, nil
}

func runAnswerTeamInvitation(c *call, args map[string]any) (output, error) {
	id, err := stringArg(args, "invitation")
	if err != nil {
		return output{}, err
	}
	answer, err := stringArg(args, "answer")
	if err != nil {
		return output{}, err
	}
	out, err := teamCall(c, "answer_team_invitation", http.MethodPost, "/v1/me/team-invitations/"+url.PathEscape(id), map[string]string{"answer": answer})
	if err != nil {
		return output{}, err
	}
	if answer == "accept" {
		return output{Text: fmt.Sprintf("You joined %v. Work on its sites with owner: %q.", out["team"], out["team"]), Structured: out}, nil
	}
	return output{Text: "Invitation declined.", Structured: out}, nil
}

func runRemoveTeamMember(c *call, args map[string]any) (output, error) {
	person, err := stringArg(args, "person")
	if err != nil {
		return output{}, err
	}
	path, err := teamPath(args, "/members/"+url.PathEscape(strings.ToLower(person)))
	if err != nil {
		return output{}, err
	}
	out, err := teamCall(c, "remove_team_member", http.MethodDelete, path, nil)
	if err != nil {
		return output{}, err
	}
	return output{Text: "Removed from the team.", Structured: out}, nil
}

func runDeleteTeam(c *call, args map[string]any) (output, error) {
	path, err := teamPath(args, "")
	if err != nil {
		return output{}, err
	}
	out, err := teamCall(c, "delete_team", http.MethodDelete, path, nil)
	if err != nil {
		return output{}, err
	}
	return output{Text: "Team deleted.", Structured: out}, nil
}
