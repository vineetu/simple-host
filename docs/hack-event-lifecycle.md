[Implementation records and state transitions](architecture/hack-domain.md).
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

## Leaving (SIM-340, 2026-10-31)

Any member can leave on their own, any time before the event ends, from the website (Overview,
Settings or a Home row menu) or through `hack_preview_leave_event` and `hack_leave_event`. A
participant leaves their team. Before the team's effective deadline, a team left with nobody on
it is deleted along with its entry and site, following the existing team-removal and
holding-account paths. Past that deadline, or once the event is closed, judging or results,
leaving never touches the team, its entry, its site or its scores, even for its last member --
the leaver is just taken off its member list, and that team is locked for good: it can never
take on a new member again, by code, link, invite or organiser placement (owner decision
2026-10-31). The teams redesign below gives this state the same `locked` flag as an ordinary
lock, but refuses ever to unlock it. A pending applicant can withdraw the
same way, and so can a rejected one -- leaving deletes their row exactly like a withdraw, and
they may apply again afterwards, where the organiser decides again (owner decision 2026-10-31,
superseding the earlier rule that a rejected applicant couldn't leave). A judge's scores stay
and keep counting exactly as they stood when they left, and an organiser can delete them with
`hack_delete_former_judge_scores` so they stop counting. The event's creator must name another
organiser as its successor first, with `successor_user_id` from the leave preview. Ending the
event instead archives it exactly as `POST .../stage {"stage":"archived"}` does, and is the only
organiser's choice when they leave. Deleting it is exactly `DELETE /v1/hack/events/{slug}` with
the slug confirmed, their other choice. Nobody can leave once the event has ended.

## Stages, the Overview button and teams (2026-10-31)

Moving the event's stage to `results` also locks judging in the same transaction, when it is
not locked already, so a stage move is enough to make Results ready to publish. Moving back to
`judging` reopens scoring. Website stage changes and publishing, including final tie order, happen only from Home. The organiser Overview shows one button for the forward move the
current stage offers (`next_step` on the organiser event view), and Change stage stays
available in the header for moving back a stage. Participants and judges see the same stage
pill and words as organisers, and the same stage track on their own home screen, with judges
keeping a second pill for their own scoring state.

Teams gained admins, locking and a shareable link the same day. The team's creator is its first
admin, and an admin can make another member admin too. The last admin can't be unmade until
someone else is one. When an admin leaves, is removed, or is moved off, the longest-standing
remaining member takes over automatically. An admin, or the organiser, can lock a team. Doing
so clears its pending join requests and invites. A locked team then stops taking anyone new
except by organiser hand placement, until it is unlocked. There is no longer a "who can join"
setting on a team: every unlocked team can be asked to join, and its link or code still joins
instantly. A new event's team size now defaults to 20, and an existing event below that was
raised to it on migration. Fill teams asks for people per team (default 4) instead of filling
every team to the event's cap.
