// Package teams implements Simple Hack team workflows independently of HTTP.
//
// Who sees team messages (sent and listed by products/hack/messages, stored by
// repository/hackteams): the team's
// current members (GET /team/messages, hack_get_my_team_messages) and the
// event's organisers (GET /teams/{team}/messages, hack_get_team_messages);
// both are told through notices. Nobody else, and not analytics.
package teams
