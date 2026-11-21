// Package teams implements Simple Hack team workflows independently of HTTP.
//
// Who sees team messages (stored and listed by app/events): the team's
// current members (GET /team/messages, hack_get_my_team_messages) and the
// event's organisers (GET /teams/{team}/messages, hack_get_team_messages);
// both are told through notices. Nobody else, and not analytics.
package teams
