// Guided walkthroughs for the new app (atlas: spec.json "walkthroughs").
// Each step points at a real element. `target` is a CSS selector tried in order;
// `phone` is used instead when the sidebar is hidden. No target means a centred card.
// Copy: plain, short, no semicolons. Edit here and in the atlas together.
export const tours = {
  organiser: {
    pref: 'organiser_walkthrough_done',
    home: 'overview',
    steps: [
      { title: 'Welcome to your event', body: 'This is Overview, your event’s home. It always says the next thing to do. Here is a one-minute look around.' },
      { target: '.status', title: 'Stage and next deadline', body: 'The stage and the next deadline are always shown here. The stage decides what participants and judges can do right now.' },
      { target: '[data-tour="change-stage"]', title: 'Move the event on', body: 'When you are ready, change the stage: open sign-up, start building, close entries, judging, results, then end. Each change says what will happen before you confirm.' },
      { target: '[data-tour="share"]', title: 'Invite people', body: 'Send the join link to participants and the judge link to judges. New link stops the old one working, so use it only if a link leaked.' },
      { target: '.nav a[href*="s=event-page"]', phone: '[data-menu]', title: 'Event page', body: 'What visitors see before they sign up: description, dates, schedule, sponsors, FAQ and announcements. You can also put up your own website.' },
      { target: '.nav a[href*="s=people"]', phone: '[data-menu]', title: 'People', body: 'Everyone who signed up. Add sign-up questions, approve applications if you want to check people first, and invite co-organisers.' },
      { target: '.nav a[href*="s=teams"]', phone: '[data-menu]', title: 'Teams', body: 'Teams as they form. Set the team size, turn team finding on or off, and place people who are still on no team.' },
      { target: '.nav a[href*="s=entries"]', phone: '[data-menu]', title: 'Entries', body: 'What each team submitted. Set the deadline, choose which fields are required, and give one team more time.' },
      { target: '.nav a[href*="s=judging"]', phone: '[data-menu]', title: 'Judging', body: 'Write the scoring rubric, choose how judges get their teams, watch progress, and lock judging when every score is in.' },
      { target: '.nav a[href*="s=results"]', phone: '[data-menu]', title: 'Results', body: 'Preview the standings, publish them, and choose what the public sees. People’s choice voting and the directory listing are here too.' },
      { target: '.nav a[href*="s=settings"]', phone: '[data-menu]', title: 'Settings', body: 'Your organiser details, website notifications, and deleting the event.' },
      { target: '[data-tour="help"]', phone: '[data-menu]', title: 'Any time', body: 'Open this walkthrough again from Help. You can also run every step from an AI app if you prefer.' }
    ]
  },
  judge: {
    pref: 'judge_walkthrough_done',
    home: 'home',
    steps: [
      { title: 'Thank you for judging', body: 'Here is how it works. It takes about 30 seconds.' },
      { target: '.status', title: 'Is scoring open?', body: 'This tells you whether you can score. Scoring opens when entries close, and ends when the organiser locks it.' },
      { target: '[data-tour="next"]', title: 'Start here', body: 'This button opens the next team you have not scored yet. When every team is scored, it says you are done.' },
      { target: '[data-tour="progress"]', title: 'Your progress', body: 'How many of your teams you have scored so far.' },
      { target: '.nav a[href*="s=queue"]', phone: '.tabbar a[href*="s=queue"]', title: 'My teams', body: 'Every team you are scoring. Scored teams move to Scored. Teams where you know someone go under Teams I know.' },
      { target: '.nav a[href*="s=rubric"]', phone: '.tabbar a[href*="s=rubric"]', title: 'Rubric & brief', body: 'What each criterion means and what the points are for. Read it once before you start.' },
      { title: 'Scoring a team', body: 'Open the team’s website, then tap a score for each criterion. Every tap saves straight away, there is no Save button. Add a comment if you like, then Save & next.' },
      { title: 'If you know someone on a team', body: 'Press I know someone on this team. The team leaves your list and your score for it stops counting. You can undo it from My teams.' },
      { target: '[data-tour="help"]', phone: '.tabbar a[href*="s=home"]', title: 'Any time', body: 'Open this walkthrough again from Help.' }
    ]
  }
}
