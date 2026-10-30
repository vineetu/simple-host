export const nav = {
  "organiser": [
    {
      "id": "overview",
      "label": "Overview",
      "icon": "home",
      "phone_tab": false,
      "stage_rule": "Always shown. This is the organiser's Home: answers what to do next at every stage, Draft to Ended."
    },
    {
      "id": "event-page",
      "label": "Event page",
      "icon": "page",
      "phone_tab": false,
      "stage_rule": "Always editable, even after the event ends (the public page stays up as a record)."
    },
    {
      "id": "people",
      "label": "People",
      "icon": "users",
      "phone_tab": false,
      "stage_rule": "Always shown. Applications badge is the pending count; reads 'Sign-up isn't open yet' before Open."
    },
    {
      "id": "teams",
      "label": "Teams",
      "icon": "team",
      "phone_tab": false,
      "stage_rule": "Always shown. Fill teams and team-finding controls disable once the event passes Building, with a note why."
    },
    {
      "id": "entries",
      "label": "Entries",
      "icon": "entries",
      "phone_tab": false,
      "stage_rule": "Always shown. The list is empty until teams start saving entries."
    },
    {
      "id": "judging",
      "label": "Judging",
      "icon": "rubric",
      "phone_tab": false,
      "stage_rule": "Always shown so the rubric can be written weeks ahead. Assignment and lock controls make sense once Closed; before that a hint explains they'll matter once entries close."
    },
    {
      "id": "results",
      "label": "Results",
      "icon": "chart",
      "phone_tab": false,
      "stage_rule": "Always shown. Publish is disabled with an explanation until judging is locked."
    },
    {
      "id": "settings",
      "label": "Settings",
      "icon": "settings",
      "phone_tab": false,
      "stage_rule": "Always shown, including Delete this event, at every stage."
    }
  ],
  "participant": [
    {
      "id": "home",
      "label": "Home",
      "icon": "home",
      "short": "Home",
      "phone_tab": true,
      "stage_rule": "Always shown once approved. Shows the pending/rejected message instead of the app if sign-up isn't approved yet."
    },
    {
      "id": "schedule",
      "label": "Schedule",
      "icon": "calendar",
      "phone_tab": false,
      "stage_rule": "Always reachable once approved; shows 'Nothing scheduled yet' until the organiser adds times."
    },
    {
      "id": "announcements",
      "label": "Announcements",
      "icon": "megaphone",
      "phone_tab": false,
      "stage_rule": "Always reachable once approved; empty state until the organiser posts one."
    },
    {
      "id": "team",
      "label": "Team",
      "icon": "team",
      "short": "Team",
      "phone_tab": true,
      "stage_rule": "Team finding (start/join/browse) shows Open through Building. From Closed on, the team panel is read-only; who's on the team still shows."
    },
    {
      "id": "entry",
      "label": "Entry",
      "icon": "file",
      "short": "Entry",
      "phone_tab": true,
      "stage_rule": "Disabled with 'Join or start a team first' until on a team. Editable Open through Closed (per-team deadline); read-only with a banner from Judging on."
    },
    {
      "id": "website",
      "label": "Website",
      "icon": "globe",
      "short": "Website",
      "phone_tab": true,
      "stage_rule": "Same gating as Entry: needs a team, editable until the deadline, read-only (view-only link) after."
    },
    {
      "id": "results",
      "label": "Results",
      "icon": "trophy",
      "phone_tab": false,
      "stage_rule": "Hidden entirely until the organiser publishes results; appears the moment they do.",
      "stages": [
        "results",
        "ended"
      ]
    }
  ],
  "judge": [
    {
      "id": "home",
      "label": "Home",
      "icon": "home",
      "short": "Home",
      "phone_tab": true,
      "stage_rule": "Always shown once the judge invite is accepted. Shows a waiting message before judging opens."
    },
    {
      "id": "queue",
      "label": "My teams",
      "icon": "queue",
      "short": "My teams",
      "phone_tab": true,
      "stage_rule": "Shows 'Judging opens 29 October' instead of a list before Closed. Lists assigned entries Closed/Judging on. Read-only once locked or after results publish."
    },
    {
      "id": "rubric",
      "label": "Rubric & brief",
      "icon": "rubric",
      "short": "Rubric",
      "phone_tab": true,
      "stage_rule": "Available as soon as the organiser saves a rubric, even before judging opens, so a judge can read it ahead of time."
    }
  ]
}
export function href(role, slug, section) {
  return '/e/' + encodeURIComponent(slug) + '/' + ({ organiser: 'manage', participant: 'team', judge: 'judge' }[role]) + (section ? '?s=' + encodeURIComponent(section) : '')
}
export function isAppRoute(path) {
  return /^\/(events(?:\/new)?|account|signin|(?:join|judge|organiser)\/[^/]+|e\/[^/]+\/(?:manage|team|judge))\/?$/.test(path)
}
export function resolveRoute(url) {
  const path = url.pathname.replace(/\/+$/, '')
  if (path === '/events/new') return { id: 'organiser-create-event' }
  if (path === '/account') return { id: 'account' }
  const invite = path.match(/^\/(join|judge|organiser)\/([^/]+)$/)
  if (invite) return { id: {join:'public-join',judge:'public-judge',organiser:'public-organiser-invite'}[invite[1]] }
  const m = path.match(/^\/e\/([^/]+)\/(manage|team|judge)$/)
  if (!m) return { id: 'landing-your-events' }
  const role = {manage:'organiser',team:'participant',judge:'judge'}[m[2]]
  const sections = nav[role].map(x => x.id).concat(role === 'organiser' ? ['team','entry','assignments'] : role === 'judge' ? ['scoring'] : [])
  const wanted = new URLSearchParams(url.search).get('s')
  const section = sections.includes(wanted) ? wanted : sections[0]
  return { id: role + '-' + section, section, slug: decodeURIComponent(m[1]) }
}
