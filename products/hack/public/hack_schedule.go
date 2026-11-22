package public

// The event's real times (start, end, entry deadline, voting window, results published) show in the schedule as fixed rows. They are computed from the settings each time, never stored, so a row can never disagree with its setting; the organiser changes them where set (set_in), not in Schedule.
// A row can also line up with a forward stage move (prompt). When its time comes the organiser's next step says so ("Kickoff is now. Open sign-up?"). It only asks: nothing here moves a stage, because a late kickoff must not close entries early.
