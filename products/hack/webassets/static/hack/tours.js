// Guided walkthroughs for the new app (atlas: spec.json "walkthroughs").
// Each step points at a real element. `target` is a CSS selector tried in order;
// `phone` is used instead when the sidebar is hidden. No target means a centred card.
// `when(ctx)` is an extra stage check for a step whose target is always in the DOM
// (the shell's own `.status` bar) but whose copy only fits some stages -- see
// tour.js canShow(). Copy: plain, short, no semicolons. Edit here and in the atlas
// together.
//
// Each tour needs exactly one seen-state field: `pref` (a server preference the
// /v1/hack/preferences API actually has -- today only organiser_walkthrough_done
// and judge_walkthrough_done) or `localKey` (localStorage, per signed-in person --
// every other tour, since the server has no field for it). See tour.js tourDone/
// markTourDone.
//
// 2026-10-30 (owner): the judge Home tour used to explain the scoring page
// (steps 7-8, no target) while the judge was still on Home, and those two
// steps showed even on an event whose results were already out, where
// scoring no longer applies at all. Fixed: Home only covers what is on
// Home, is stage-aware about scoring, and the scoring page gets its own
// tour (judgeScoring) that opens on the scoring page itself. Same review
// applied to the new participant tours.
function judgeEvent(ctx) { return (ctx.event && ctx.event.event) || {} }
function scoringOpen(ctx) { const ev = judgeEvent(ctx); return !!ev.scoring_open && !ev.judging_locked_at }
function scoringNotYetOpen(ctx) { const ev = judgeEvent(ctx); return !ev.scoring_open && !ev.judging_locked_at && !ev.results_published }
// Review #4: "Start here" rings judge-home's one hero button, which is
// either "Score next" (more teams to score) or "Review my scores" (every
// team already scored) -- the events list has no field for which one, so
// the two steps tell the two states apart by the hero's own heading, the
// same text already on screen.
function judgeHasNextTeam(ctx) { const k = document.querySelector('[data-tour="next"] .k'); return !!k && k.textContent.trim() === 'Your next step' }
function judgeAllScored(ctx) { const k = document.querySelector('[data-tour="next"] .k'); return !!k && k.textContent.trim() === 'All done' }

export const tours = {
  organiser: {
    pref: 'organiser_walkthrough_done',
    home: 'overview',
    steps: [
      { title: 'Welcome to your event', body: 'This is Overview, your event’s home. It always says the next thing to do. Here is a one-minute look around.' },
      { target: '.status', title: 'Stage and next deadline', body: 'The stage and the next deadline are always shown here. The stage decides what participants and judges can do right now.' },
      // Teams redesign (owner, 2026-10-31): the next-step card now has one
      // button that moves the event forward itself; Change stage stays for
      // any other move, so its own step is re-worded to say so instead of
      // claiming to be the only way to move the event on.
      { target: '[data-tour="next-move"]', title: 'Your next step', body: 'This button opens the next checklist task or moves the event forward. Stage changes ask you to confirm.' },
      // SIM-394: hidden by mountAssistant (assistant.js) when the server has
      // no assistant configured -- canShow()/resolveTarget() in tour.js
      // already skip any step whose target isn't on screen, so this step
      // simply doesn't show for that organiser rather than needing its own
      // check here.
      { target: '[data-tour="assistant"]', title: 'Ask about your event', body: 'Want to know more about a setting? Ask here, from any screen. It answers in a line or two and links to the place to change it.' },
      { target: '[data-tour="change-stage"]', title: 'Move the event on', body: 'To move to any other stage, or back, use Change stage. Each change says what will happen before you confirm.' },
      { target: '[data-tour="share"]', title: 'Invite people', body: 'Send the join link to participants and the judge link to judges. Make a new link in Settings → Links if a link leaked.' },
      // Atlas 2026-10-30: Event page is now sub-pages (Basics, About,
      // Schedule, Sponsors, FAQ, Look, Website), and Announcements moved to
      // its own sidebar item right under it -- both steps still point at
      // real nav links, just the Event page one no longer claims
      // Announcements lives there, and there is a new step for where it
      // actually is.
      { target: '.nav a[href*="s=event-page"]', phone: '[data-menu]', title: 'Event page', body: 'What visitors see before they sign up, one piece at a time: basics, about, schedule, sponsors, FAQ, look, and your own website if you have one.' },
      { target: '.nav a[href*="s=announcements"]', phone: '[data-menu]', title: 'Announcements', body: 'Post updates for everyone in the event. Tick the box to email every participant too.' },
      { target: '.nav a[href*="s=settings"]', phone: '[data-menu]', title: 'Settings', body: 'Set sign-up, team and entry rules, judging, voting, result visibility and directory listing here. Organiser details and notifications live here too.' },
      { target: '.nav a[href*="s=people"]', phone: '[data-menu]', title: 'People', body: 'Review applications and manage the people who signed up.' },
      { target: '.nav a[href*="s=teams"]', phone: '[data-menu]', title: 'Teams', body: 'Watch teams form and place people who are not on a team yet.' },
      { target: '.nav a[href*="s=entries"]', phone: '[data-menu]', title: 'Entries', body: 'Review entries and give one team more time.' },
      { target: '.nav a[href*="s=judging"]', phone: '[data-menu]', title: 'Judging', body: 'Assign judges, watch progress, and finish judging from Home when every score is in.' },
      { target: '.nav a[href*="s=results"]', phone: '[data-menu]', title: 'Results', body: 'Preview the standings, choose what the public page shows and download exports. Publish results from Home.' },
      { target: '[data-tour="help"]', phone: '[data-menu]', title: 'Any time', body: 'Open this walkthrough again from Help.' }
    ]
  },
  // This tour only covers what is on the judge's Overview screen (renamed
  // from Home 2026-10-30 -- see the participant tour's comment below). It
  // used to also explain the scoring page (two steps with no target, so they
  // always showed, even once results were out and scoring no longer
  // applied) -- that explanation now lives on the scoring page itself
  // (tours.judgeScoring), and Overview's own "is scoring open?" step changes
  // with the stage instead of always talking about scoring.
  judge: {
    pref: 'judge_walkthrough_done',
    home: 'home',
    steps: [
      { title: 'Thank you for judging', body: 'Here is how it works. It takes about 30 seconds.' },
      // Exactly one of these two shows (see `when`): the shell's `.status`
      // bar is always in the DOM, so a target-presence check alone could
      // never tell "open" apart from "not open yet", and neither should
      // show once judging is locked or results are out -- at that point
      // the stage banner on Home already says so, and this tour has
      // nothing to add about scoring.
      { target: '.status', when: scoringOpen, title: 'Is scoring open?', body: 'This tells you whether you can score. It stays open until the organiser finishes judging.' },
      { target: '.status', when: scoringNotYetOpen, title: 'Is scoring open?', body: 'It opens when the organiser starts judging. This bar shows it.' },
      { target: '[data-tour="next"]', when: judgeHasNextTeam, title: 'Start here', body: 'Score next opens the next team you haven’t scored.' },
      { target: '[data-tour="next"]', when: judgeAllScored, title: 'Start here', body: 'Review my scores opens My teams, where you can change any score.' },
      { target: '[data-tour="progress"]', title: 'Your progress', body: 'How many of your teams you have scored so far.' },
      { target: '.nav a[href*="s=queue"]', phone: '.tabbar a[href*="s=queue"]', title: 'My teams', body: 'Every team you are scoring. Scored teams move to Scored. Teams where you know someone go under Teams I know.' },
      { target: '.nav a[href*="s=rubric"]', phone: '.tabbar a[href*="s=rubric"]', title: 'Rubric & brief', body: 'What each criterion means and what the points are for. Read it once before you start.' },
      { target: '[data-tour="help"]', phone: '.tabbar [data-menu]', title: 'Any time', body: 'Open this walkthrough again from Help.' }
    ]
  },
  // Opens automatically the first time a judge reaches the scoring page
  // while scoring is open (judge-scoring.js), and from Help's "Replay
  // walkthrough" while on that page. No server preference exists for it,
  // so the seen-state is localStorage (see tour.js).
  judgeScoring: {
    localKey: 'sh-judge-scoring-tour-done',
    home: 'scoring',
    steps: [
      // Entries slice 2: the card, the video and the site are all on this
      // screen now; each step is skipped when its target isn't there (no
      // video, no site).
      { target: '[data-tour="card"]', title: 'Start with the card', body: 'The team’s problem, solution, who it is for and what works today. A 20-second read.' },
      { target: '[data-tour="video"]', title: 'Watch the demo', body: 'Press play to watch it here. Nothing loads until you do.' },
      { target: '[data-tour="site"]', title: 'See what they built', body: 'The team’s website as they submitted it. Use Open for a new tab.' },
      { target: '[data-tour="score"]', title: 'Score each criterion', body: 'Tap a score. It saves straight away.' },
      // Cosmetic #3 (retest 3): side:'left' keeps this card from landing on
      // top of the Execution/Design score rows above the comment box --
      // see tour.js's place().
      { target: '[data-tour="comment"]', side: 'left', title: 'Add a comment', body: 'Add feedback once you score. The team sees it without your name.' },
      { target: '[data-tour="save-next"]', title: 'Save & next', body: 'Your scores already saved themselves. This just moves you to the next team. On your last team it reads Save & finish. On a keyboard, N does the same and P goes back.' },
      { target: '[data-tour="conflict"]', title: 'Know someone on this team?', body: 'If you know someone on this team, press this. The team leaves your list and your score for it stops counting. You can undo it from My teams.' },
      { target: '[data-tour="prev"]', title: 'Previous', body: 'Go back to the team before this one. Disabled on your first team.' }
    ]
  },
  // This tour only covers what is on the participant's Overview screen (renamed
  // from Home 2026-10-30 -- "Home" now means only the person's own home above
  // all their events, in shell.js): the sidebar items, the next-step block, and
  // the stage/deadline status bar. The Team screen's own controls (team
  // finding, starting or joining a team) get their own tour
  // (tours.participantTeam) that opens on that screen.
  participant: {
    localKey: 'sh-participant-tour-done',
    home: 'home',
    steps: [
      { title: 'Welcome', body: 'This is Overview. It always says the next thing to do. Here is a one-minute look around.' },
      { target: '.status', title: 'Stage and deadline', body: 'The event stage and the next deadline are always shown here.' },
      { target: '[data-tour="next"]', title: 'Your next step', body: 'This always shows what to do next for where the event is now.' },
      // Atlas "entries and participant journey" package (2026-11-02): the
      // checklist card -- team, summary card, show your work, submit -- with
      // its own live countdown to the deadline.
      { target: '[data-tour="entry-checklist"]', title: 'Your entry', body: 'The four steps to a submitted entry, with a live countdown to your deadline.' },
      { target: '.nav a[href*="s=schedule"]', phone: '[data-menu]', title: 'Schedule', body: 'What is happening and when.' },
      { target: '.nav a[href*="s=announcements"]', phone: '[data-menu]', title: 'Announcements', body: 'Updates from the organiser.' },
      { target: '.nav a[href*="s=team"]', phone: '.tabbar a[href*="s=team"]', title: 'Team', body: 'Find or start your team here. If team finding is on, you can see teams looking for people.' },
      { target: '.nav a[href*="s=entry"]', phone: '.tabbar a[href*="s=entry"]', title: 'Entry', body: 'Fill in what you built. Teammates can edit it too. Judges see this.' },
      { target: '.nav a[href*="s=website"]', phone: '.tabbar a[href*="s=website"]', title: 'Website', body: 'Publish your team’s site from an AI app or upload a zip.' },
      { target: '.nav a[href*="s=results"]', phone: '[data-menu]', title: 'Results', body: 'See the winners and how your team placed.' },
      { target: '[data-tour="help"]', phone: '.tabbar [data-menu]', title: 'Any time', body: 'Open this walkthrough again from Help.' }
    ]
  },
  // Opens automatically the first time a participant with no team opens
  // the Team screen while teams are open (participant-team.js). Every step
  // only shows when its target is on screen -- team finding being off
  // hides the open-teams list and Be found, so those steps just don't
  // appear, same as any other tour.
  // Teams redesign (owner, 2026-10-31): rewritten for the new "How do you
  // want to team up?" page -- the side-by-side Start/Join cards this tour
  // used to point at (data-tour="start-team"/"join-team") are gone, replaced
  // by the one choices group (spec.json walkthroughs.participantTeam).
  participantTeam: {
    localKey: 'sh-participant-team-tour-done',
    home: 'team',
    steps: [
      { target: '[data-tour="team-ways"]', title: 'Three ways onto a team', body: 'Ask a team that’s looking, join with a link or code a teammate sent you, or start your own.' },
      { target: '[data-tour="open-teams"]', title: 'Ask to join', body: 'Teams looking for people. Ask one, or several. Their admins say yes or no.' },
      { target: '[data-tour="be-found"]', title: 'Let teams find you', body: 'List yourself with a one-line intro. Teams can invite you, and you choose.' }
    ]
  }
}
