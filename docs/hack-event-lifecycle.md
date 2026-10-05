# Simple Hack event lifecycle

Status: shipped 2026-10-05.

An organiser creates a draft, opens sign-up, lets teams build, closes entries,
judges and publishes results, then ends the event. Ending is final for the
organiser. Archived team sites normally remain for 30 days before the cleanup
sweep moves them to Recently deleted; the public event page and results remain.
The platform admin can keep those sites longer through the existing tools.

To remove the entire event, a current organiser opens **Manage → Settings →
Delete this event**, reads what will go, types the exact event address name and
presses **Delete this event** in the in-page dialog. Cancel or Escape leaves the
event intact. This works at every stage, including Ended. Participants and judges
cannot delete events; the existing platform take-down restriction still applies.

Deletion permanently removes the public page, results, team sites, custom website,
entries, scores, votes, storage, memberships and team keys. Sites take the existing
team-removal path before the holding-account deletion purges that account's files,
including Recently deleted. There is no event undo and no deletion email.
The event disappears from the directory and Your events. Old event links show
**Event not found**, and old join/judge links no longer work.

An event's address name remains taken after a participant or judge has joined,
even if they were later removed or no team was formed. A never-joined event still
frees its name. Existing team-origin reservations remain permanent.

The organiser API is `DELETE /v1/hack/events/{slug}` with
`{"confirm":"<slug>"}`. Missing confirmation on a busy/ended event and explicitly
wrong confirmation return 409 `delete_only_empty` with the required body explained.
Empty, unended events still accept no body. The destructive `hack_delete_event`
connector tool forwards `body.confirm`; run-hackathon always asks the person first.
The platform admin keeps its existing separate delete route.
