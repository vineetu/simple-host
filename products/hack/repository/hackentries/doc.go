// Package hackentries owns Simple Hack's entries and judging: event_entries
// (and their screenshots), event_entry_receipts, event_entry_reminders,
// event_judge_reminders, rubric_criteria, event_scores, event_former_judges,
// event_assignments, event_conflicts, event_track_panels, event_results and
// event_votes (one vote per event and account, voter_id). Other packages call
// these functions; sqlcheck -owners enforces it.
//
// Import order: hackevents (bottom) <- hackmembers <- hackteams <-
// hackentries. This package is the top: it asks the three below for events,
// members and team rows (hackteams/entries_door.go) and joins its own rows to
// them by id.
package hackentries
