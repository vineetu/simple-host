// Package hackmembers owns who is in a Simple Hack event: event_members (role,
// approval, team, team admin, looking), event_notices (the lines and emails
// that tell a person something waits on them) and hack_account_preferences.
//
// Member is the one read of one person's membership (with the lock the
// caller needs); Members, IDs and Count read sets through a Filter. The
// predicates on model.EventMember (Organiser, Participant, Approved, Judge)
// are the one statement of what a role may do.
//
// Other packages call these functions; sqlcheck -owners enforces it.
//
// Import order: hackevents (bottom) <- hackmembers <- hackteams <-
// hackentries. This package asks hackevents for event facts.
package hackmembers
