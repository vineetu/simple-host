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
function scoringOpen(ctx) { const ev = judgeEvent(ctx); return !!ev.scoring_open && !ev.judging_locked_at && !ev.results_published }
function scoringNotYetOpen(ctx) { const ev = judgeEvent(ctx); return !ev.scoring_open && !ev.judging_locked_at && !ev.results_published }

export const tours = {
  organiser: {
    pref: 'organiser_walkthrough_done',
    home: 'overview',
    steps: [
      { title: 'Welcome to your event', body: 'This is Overview, your event’s home. It always says the next thing to do. Here is a one-minute look around.' },
      { target: '.status', title: 'Stage and next deadline', body: 'The stage and the next deadline are always shown here. The stage decides what participants and judges can do right now.' },
      { target: '[data-tour="change-stage"]', title: 'Move the event on', body: 'When you are ready, change the stage: open sign-up, start building, close entries, judging, results, then end. Each change says what will happen before you confirm.' },
      { target: '[data-tour="share"]', title: 'Invite people', body: 'Send the join link to participants and the judge link to judges. Use New link only if a link leaked, because it stops the old one.' },
      { target: '.nav a[href*="s=event-page"]', phone: '[data-menu]', title: 'Event page', body: 'What visitors see before they sign up: description, dates, schedule, sponsors, FAQ and announcements. You can also put up your own website.' },
      { target: '.nav a[href*="s=people"]', phone: '[data-menu]', title: 'People', body: 'Everyone who signed up. Add sign-up questions, approve applications if you want to check people first, and invite co-organisers.' },
      { target: '.nav a[href*="s=teams"]', phone: '[data-menu]', title: 'Teams', body: 'Teams as they form. Set the team size, turn team finding on or off, and place people who are not on a team yet.' },
      { target: '.nav a[href*="s=entries"]', phone: '[data-menu]', title: 'Entries', body: 'What each team submitted. Set the deadline, choose which fields are required, and give one team more time.' },
      { target: '.nav a[href*="s=judging"]', phone: '[data-menu]', title: 'Judging', body: 'Write the scoring rubric, choose how judges get their teams, watch progress, and lock judging when every score is in.' },
      { target: '.nav a[href*="s=results"]', phone: '[data-menu]', title: 'Results', body: 'Preview the standings, publish them, and choose what the public sees. People’s choice voting and the directory listing are here too.' },
      { target: '.nav a[href*="s=settings"]', phone: '[data-menu]', title: 'Settings', body: 'Your organiser details, website notifications, and deleting the event.' },
      { target: '[data-tour="help"]', phone: '[data-menu]', title: 'Any time', body: 'Open this walkthrough again from Help.' }
    ]
  },
  // Home only covers what is on Home. It used to also explain the scoring
  // page (two steps with no target, so they always showed, even once
  // results were out and scoring no longer applied) -- that explanation
  // now lives on the scoring page itself (tours.judgeScoring), and Home's
  // own "is scoring open?" step changes with the stage instead of always
  // talking about scoring.
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
      { target: '.status', when: scoringOpen, title: 'Is scoring open?', body: 'This tells you whether you can score. It stays open until the organiser locks judging.' },
      { target: '.status', when: scoringNotYetOpen, title: 'Is scoring open?', body: 'Not yet. It opens once entries close -- this bar will say the moment it does.' },
      { target: '[data-tour="next"]', title: 'Start here', body: 'We’ll show you around the scoring page when you open it.' },
      { target: '[data-tour="progress"]', title: 'Your progress', body: 'How many of your teams you have scored so far.' },
      { target: '.nav a[href*="s=queue"]', phone: '.tabbar a[href*="s=queue"]', title: 'My teams', body: 'Every team you are scoring. Scored teams move to Scored. Teams where you know someone go under Teams I know.' },
      { target: '.nav a[href*="s=rubric"]', phone: '.tabbar a[href*="s=rubric"]', title: 'Rubric & brief', body: 'What each criterion means and what the points are for. Read it once before you start.' },
      { target: '[data-tour="help"]', phone: '[data-menu]', title: 'Any time', body: 'Open this walkthrough again from Help.' }
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
      { target: '[data-tour="site"]', title: 'See what they built', body: 'Open the team’s website to see their project.' },
      { target: '[data-tour="score"]', title: 'Score each criterion', body: 'Tap a score. It saves straight away.' },
      { target: '[data-tour="comment"]', title: 'Add a comment', body: 'Optional feedback for the team. They see it without your name.' },
      { target: '[data-tour="save-next"]', title: 'Save & next', body: 'Your scores already saved themselves -- this just moves you to the next team.' },
      { target: '[data-tour="conflict"]', title: 'Know someone on this team?', body: 'If you know someone on this team, press this. The team leaves your list and your score for it stops counting. You can undo it from My teams.' },
      { target: '[data-tour="prev"]', title: 'Previous', body: 'Go back to the team before this one.' }
    ]
  },
  // Home only covers what is on Home: the sidebar items, the next-step
  // block, and the stage/deadline status bar. The Team screen's own
  // controls (team finding, starting or joining a team) get their own
  // tour (tours.participantTeam) that opens on that screen.
  participant: {
    localKey: 'sh-participant-tour-done',
    home: 'home',
    steps: [
      { title: 'Welcome', body: 'This is Home. It always says the next thing to do. Here is a one-minute look around.' },
      { target: '.status', title: 'Stage and deadline', body: 'The event stage and the next deadline are always shown here.' },
      { target: '[data-tour="next"]', title: 'Your next step', body: 'This always says the one thing to do next.' },
      { target: '.nav a[href*="s=schedule"]', phone: '[data-menu]', title: 'Schedule', body: 'What is happening and when.' },
      { target: '.nav a[href*="s=announcements"]', phone: '[data-menu]', title: 'Announcements', body: 'Updates from the organiser.' },
      { target: '.nav a[href*="s=team"]', phone: '.tabbar a[href*="s=team"]', title: 'Team', body: 'Find or start your team here. If team finding is on, you can see teams looking for people.' },
      { target: '.nav a[href*="s=entry"]', phone: '.tabbar a[href*="s=entry"]', title: 'Entry', body: 'Fill in what you built. Teammates can edit it too. Judges see this.' },
      { target: '.nav a[href*="s=website"]', phone: '.tabbar a[href*="s=website"]', title: 'Website', body: 'Publish your team’s site from an AI app or upload a zip.' },
      { target: '.nav a[href*="s=results"]', phone: '[data-menu]', title: 'Results', body: 'Appears here the moment the organiser publishes them.' },
      { target: '[data-tour="help"]', phone: '[data-menu]', title: 'Any time', body: 'Open this walkthrough again from Help.' }
    ]
  },
  // Opens automatically the first time a participant with no team opens
  // the Team screen while teams are open (participant-team.js). Every step
  // only shows when its target is on screen -- team finding being off
  // hides the open-teams list and Be found, so those steps just don't
  // appear, same as any other tour.
  participantTeam: {
    localKey: 'sh-participant-team-tour-done',
    home: 'team',
    steps: [
      { target: '[data-tour="open-teams"]', title: 'Find a team', body: 'Ask to join a team that’s looking for people.' },
      { target: '[data-tour="start-team"]', title: 'Start your own', body: 'Give it a name. You’re the first member, and you can share the code with teammates.' },
      { target: '[data-tour="join-team"]', title: 'Have a code?', body: 'Type the code a teammate shared with you to join their team.' },
      { target: '[data-tour="be-found"]', title: 'Be found', body: 'List yourself as looking for a team and write a one-line intro. Teams can invite you.' }
    ]
  }
}
