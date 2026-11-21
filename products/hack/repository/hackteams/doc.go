// Package hackteams owns Simple Hack's teams: event_teams (the team row, its
// code, deadline, lock, pin, track and site take-down), event_team_requests
// (join requests and invitations) and event_team_messages (the thread
// between a team and the organisers; sender names come from the account by
// sender_id). Other packages call these functions; sqlcheck -owners
// enforces it.
//
// Locks: LockTeamRow (one team) and LockEventTeams (every team of an event,
// in id order), taken after the event and member rows. The entries owner
// asks for team rows through entries_door.go (JudgedTeams, LiveTeams,
// TeamsByIDs) and joins its own rows to them by id.
//
// Import order: hackevents (bottom) <- hackmembers <- hackteams <-
// hackentries. This package calls hackevents and hackmembers for event and
// membership facts, never hackentries. event_site.go (ReadSiteBindings,
// OwnerOnSite) runs on every storage request, so each is one statement
// joining the events and event_members rows: listed exceptions.
package hackteams
