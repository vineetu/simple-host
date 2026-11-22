// Package hackevents owns the Hack event tables: events (the event row and
// its settings, including voting), event_tracks, event_used_names,
// event_announcements and their deliveries, the checklist marks,
// event_content, event_create_log and event_organiser_invites (the
// assistant's spend and usage are products/hack/assistant's). Other packages call these functions;
// sqlcheck -owners enforces it. deadline.go holds the team deadline rule the
// team and entry queries use, with the event's deadline passed in.
//
// Import order: hackevents (bottom) <- hackmembers <- hackteams <-
// hackentries. This package imports none of them; who is in an event is
// hackmembers', teams are hackteams', entries and reminders hackentries'.
package hackevents
