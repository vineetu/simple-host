// Package teams is teams and their invitations: an account that owns sites
// and that people join, where accounts own their own sites. A person creates
// a team, invites by email, answers an invitation addressed to their
// verified email, removes members and deletes the team (routes.go, logic in
// teams.go). Who may act for a team is access.ActsAsOwner; the team tables
// stay with the core's repo, which that check, the GET /v1/me teams and
// host/ops's team deletion read.
package teams
