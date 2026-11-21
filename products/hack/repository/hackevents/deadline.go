package hackevents

// The team deadline rule, in one place. A team's own deadline
// (event_teams t.deadline_override) only ever extends the event's submission
// deadline, and an event without a deadline gives its teams none. Team and
// entry queries take the event's deadline as a parameter, e.g.
// EffectiveDeadline("$2::timestamptz"), read from the event first.

// EffectiveDeadline is the SQL for team t's deadline, given the SQL for its
// event's submission deadline.
func EffectiveDeadline(eventDeadline string) string {
	return `CASE WHEN ` + eventDeadline + ` IS NULL THEN NULL ELSE GREATEST(` + eventDeadline + `, t.deadline_override) END`
}

// EmptyTeamDeletable is the SQL predicate under which team t, once nobody is
// on it, is deleted rather than kept for its submission: it holds no pin and
// its deadline has not passed.
func EmptyTeamDeletable(eventDeadline string) string {
	d := EffectiveDeadline(eventDeadline)
	return `t.pinned_at IS NULL AND (` + d + ` IS NULL OR ` + d + ` > clock_timestamp())`
}

// FrozenStage is whether the stage freezes every team, whatever its deadline.
func FrozenStage(stage string) bool {
	return stage == "judging" || stage == "results" || stage == "archived"
}

// effectiveDeadlineSQL is EffectiveDeadline over events e, for this
// package's own queries that join a team to its event.
var effectiveDeadlineSQL = EffectiveDeadline("e.submission_deadline")
