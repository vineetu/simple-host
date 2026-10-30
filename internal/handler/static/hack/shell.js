import { nav, href } from './nav.js'
import { esc, stageLabel, roleLabel, initials, nextDeadline, dateTime, copyButton, markInk } from './ui.js'
  const I = {
    home:'M3 10.5 10 4l7 6.5V17a1 1 0 0 1-1 1h-3.5v-5h-5v5H4a1 1 0 0 1-1-1z',
    calendar:'M4 5h12a1 1 0 0 1 1 1v10a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V6a1 1 0 0 1 1-1zm-1 4h14M7 3v4m6-4v4',
    megaphone:'M3 8v4h3l6 4V4L6 8zm12-1.5a4 4 0 0 1 0 7',
    users:'M7.5 9a3 3 0 1 0 0-6 3 3 0 0 0 0 6zM2 17c0-3 2.5-5 5.5-5s5.5 2 5.5 5m1-8a2.5 2.5 0 1 0 0-5m1.5 8c2 .4 3.5 2.2 3.5 5',
    team:'M10 9a3 3 0 1 0 0-6 3 3 0 0 0 0 6zM4 17c0-3.3 2.7-5.5 6-5.5s6 2.2 6 5.5',
    file:'M5 2h7l4 4v11a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V3a1 1 0 0 1 1-1zm7 0v4h4M7 10h6M7 13h6',
    globe:'M10 18a8 8 0 1 0 0-16 8 8 0 0 0 0 16zM2 10h16M10 2c2.2 2.2 3.3 4.9 3.3 8s-1.1 5.8-3.3 8c-2.2-2.2-3.3-4.9-3.3-8S7.8 4.2 10 2z',
    trophy:'M6 3h8v4a4 4 0 0 1-8 0zm0 1H3v1.5A2.5 2.5 0 0 0 5.5 8M14 4h3v1.5A2.5 2.5 0 0 1 14.5 8M10 11v3m-3 3h6m-5-3h4',
    queue:'M4 5h12M4 10h12M4 15h7',
    star:'M10 2.8l2.2 4.6 5 .7-3.6 3.5.9 5-4.5-2.4-4.5 2.4.9-5L2.8 8.1l5-.7z',
    rubric:'M5 3h10a1 1 0 0 1 1 1v13H4V4a1 1 0 0 1 1-1zm2 4h6M7 10h6M7 13h3',
    settings:'M10 12.5a2.5 2.5 0 1 0 0-5 2.5 2.5 0 0 0 0 5zm6.5-2.5-.1-1.2 1.6-1.3-1.6-2.8-2 .6a6 6 0 0 0-2-1.2L12 2H8l-.4 2.1a6 6 0 0 0-2 1.2l-2-.6L2 7.5l1.6 1.3a6 6 0 0 0 0 2.4L2 12.5l1.6 2.8 2-.6a6 6 0 0 0 2 1.2L8 18h4l.4-2.1a6 6 0 0 0 2-1.2l2 .6 1.6-2.8-1.6-1.3z',
    page:'M3 4h14v12H3zM3 8h14M6 6h.01',
    entries:'M4 4h5v5H4zM11 4h5v5h-5zM4 11h5v5H4zM11 11h5v5h-5z',
    chart:'M4 16V9m4 7V5m4 11v-5m4 5V7',
    vote:'M4 10l4 4 8-8',
    people:'M10 9a3 3 0 1 0 0-6 3 3 0 0 0 0 6zm-6 8c0-3.3 2.7-5.5 6-5.5s6 2.2 6 5.5',
    more:'M5 10h.01M10 10h.01M15 10h.01',
    user:'M10 9a3 3 0 1 0 0-6 3 3 0 0 0 0 6zm-6 8c0-3.3 2.7-5.5 6-5.5s6 2.2 6 5.5',
    help:'M10 18a8 8 0 1 0 0-16 8 8 0 0 0 0 16zM8 7.5a2 2 0 1 1 2.8 1.8c-.5.3-.8.7-.8 1.2V11m0 3h.01',
    events:'M3 5h14M3 10h14M3 15h14',
    chev:'M6 8l4 4 4-4'
  };
  export function ico(n,cls){ return '<svg class="'+(cls||'ico')+'" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="'+(I[n]||I.file)+'"/></svg>'; }

export function eventOrder(events) {
  // The list endpoint has role and stage, but no pending-work counts.
  const attention = e => !e.taken_down && ((e.role === 'organiser' && !['archived','ended'].includes(e.stage)) || (e.role === 'judge' && ['closed','judging'].includes(e.stage)))
  return [...events].sort((a,b) => Number(attention(b)) - Number(attention(a)))
}
export function mount(ctx, section, screenId) {
  const { event: detail, role, events, me } = ctx
  const event = detail?.event
  const items = (nav[role] || []).filter(it => !it.stages || (event?.results_published && it.stages.includes(event.stage === 'archived' ? 'ended' : event?.stage)))
  function link(it) {
    const disabled = role === 'participant' && ['entry','website'].includes(it.id) && !detail?.me?.team
    return `<a href="${esc(href(role,event.slug,it.id))}"${section === it.id ? ' aria-current="page"' : ''}${disabled ? ' class="later" aria-disabled="true" title="Join or start a team first"' : ''}>${ico(it.icon)}<span>${esc(it.label)}</span></a>`
  }
  // Top-left brand: the one thing that always leads Home (/events), from any
  // screen (owner decision 2026-10-30). Replaces the switcher's old top-left
  // spot -- the switcher now heads the event section below instead, and only
  // when an event is actually open.
  const personName = me?.display_name || me?.username || me?.email || ''
  function brand() {
    return `<a class="brand" href="/events"><span class="mark">simple<b>·</b>hack</span>${personName ? `<span class="person">${esc(personName)}</span>` : ''}</a>`
  }
  const topNav = `<nav class="top-nav" aria-label="Home"><a href="/events"${screenId === 'landing-your-events' ? ' aria-current="page"' : ''}>${ico('home')}<span>Home</span></a><a href="/directory">${ico('globe')}<span>Find a hackathon</span></a></nav>`
  function switcher(id) {
    return `<button class="switcher" id="${id}" aria-expanded="false" aria-controls="evpop"><span class="ev-mark">${esc(initials(event?.title || 'Simple Hack'))}</span><span><span class="ev-name">${esc(event?.title || 'Simple Hack')}</span><span class="ev-role">${esc(roleLabel(role))}</span></span>${ico('chev','chev')}</button>`
  }
  // Owner decision 2026-10-30: the dropdown only lists events now -- Home and
  // Find a hackathon already live in the sidebar's always-shown top section,
  // so the old "More" block here just repeated them.
  const popup = `<div class="menu-pop" id="evpop"><div class="lbl">Your events</div>${eventOrder(events).map(e => `<a href="${esc(href(e.role,e.slug))}"><span class="ev-mark">${esc(initials(e.title))}</span><span>${esc(e.title)}<small>${esc(roleLabel(e.role))} · ${esc(e.taken_down ? 'Taken down' : stageLabel(e.stage))}</small></span></a>`).join('')}</div>`
  const tabs = items.filter(it => it.phone_tab)
  // A18: the tab bar's own More link and the top "Menu" button both opened
  // the same sheet. Keep one control: More always sits in the tab bar
  // (Account and Help live inside its sheet); kit.css hides the top button
  // whenever a tab bar is present.
  const tabbar = role && role !== 'organiser' ? `<nav class="tabbar" aria-label="Sections">${tabs.map(it => `<a href="${esc(href(role,event.slug,it.id))}"${section === it.id ? ' aria-current="page"' : ''}>${ico(it.icon)}<span>${esc(it.short || it.label)}</span></a>`).join('')}<a href="#" data-menu>${ico('more')}<span>More</span></a></nav>` : ''
  // On phone, inside an event the top bar shows the switcher (context + a
  // quick jump to another event) instead of the brand; outside an event
  // (Home, Account, Create event) there is nothing to switch, so it shows
  // the brand instead -- still one tap from Menu either way.
  const phoneTop = event ? switcher('sw2') : brand()
  const next = nextDeadline(event)
  const track = ['draft','open','building','closed','judging','results','archived']
  // replay-organiser-walkthrough / replay-judging-walkthrough /
  // replay-scoring-walkthrough / replay-participant-walkthrough: Help always
  // offers a way back into the current screen's own walkthrough, even once
  // it's been dismissed, by deep-linking to the screen that owns it -- the
  // scoring page's tour from the scoring page (review #6/#7: only while
  // scoring is actually open to score, since a locked/not-open visit has
  // none of that tour's targets; and carrying the team already open, so
  // Replay doesn't jump to a different team), the Team page's tour from
  // Team (review #7: only while this person still has no team to find --
  // with a team, Team's own tour has nothing to point at), Home's own tour
  // everywhere else (organiser only ever has the one, on Overview).
  const judgeScoringOpen = !!(event && event.scoring_open && !event.judging_locked_at && !event.results_published)
  const walkthroughSection = role === 'organiser' ? 'overview'
    : role === 'judge' ? (section === 'scoring' && judgeScoringOpen ? 'scoring' : 'home')
    : role === 'participant' ? (section === 'team' && !detail?.me?.team ? 'team' : 'home')
    : ''
  // Review #16: a pending/rejected participant never reaches a screen
  // module (app.js replaces it with the lock card first), so Replay
  // walkthrough would do nothing for them -- don't offer it.
  const participantLocked = role === 'participant' && detail?.me?.approval_status && detail.me.approval_status !== 'approved'
  const walkthroughTeam = walkthroughSection === 'scoring' && ctx.params.get('team')
    ? '&team=' + encodeURIComponent(ctx.params.get('team'))
    : ''
  const walkthroughHref = event && walkthroughSection && !participantLocked
    ? href(role,event.slug,walkthroughSection) + '&walkthrough=1' + walkthroughTeam
    : ''
  // The judge pill has its own wording while scoring isn't open yet --
  // "Not open yet" is also the Draft stage's own label (A21/shell.js#2), so
  // judges get a phrase that can't be confused with the event's stage.
  const status = event ? `<div class="status"><span class="pill stage"><span class="d"></span>${esc(role === 'judge' ? (event.judging_locked_at ? 'Scoring locked' : event.scoring_open ? 'Scoring open' : 'Scoring not open') : stageLabel(event.stage))}</span>${next ? `<span class="muted">${esc(next[0])} · ${esc(dateTime(next[1],event))}</span>` : ''}<span class="grow"></span>${role === 'organiser' && section === 'overview' ? `<div class="stages">${track.map((s,i) => `<span class="${s === event.stage ? 'now' : i < track.indexOf(event.stage) ? 'done' : ''}">${esc(stageLabel(s))}</span>`).join('')}</div>` : ''}</div>` : ''
  const root = document.getElementById('app')
  // Two sidebar levels (owner decision 2026-10-30): the top-nav (Home, Find a
  // hackathon) is always shown; the event section (switcher + role nav) only
  // exists while an event is open -- on Home itself (no event) only the top
  // section shows.
  const eventNav = event ? `<div class="event-nav">${switcher('sw1')}<nav class="nav" aria-label="Event">${items.map(link).join('')}</nav></div>` : ''
  root.innerHTML = `<div class="app${tabbar ? ' has-tabs' : ''}"><aside class="side" aria-label="Navigation">${brand()}${topNav}${eventNav}<div class="side-foot"><a href="/account">${ico('user')}<span>Account</span></a><details><summary data-tour="help">${ico('help')}<span>Help</span></summary><a href="/get-started">Get started</a>${walkthroughHref ? `<a href="${esc(walkthroughHref)}">Replay walkthrough</a>` : ''}<a href="mailto:support@simple-host.app">Contact support</a></details></div></aside>${event ? popup : ''}<div class="col"><div class="phone-top">${phoneTop}<button class="menu-btn" data-menu>Menu</button></div>${status}<div class="content"><main class="page"></main></div></div>${tabbar}</div>`
  const app = root.firstElementChild
  // On Home itself (no event) there is no switcher and no popup to toggle --
  // pop is null there, so every branch below that touches it is guarded.
  const pop = root.querySelector('#evpop')
  app.addEventListener('click', e => {
    const sw = e.target.closest('.switcher')
    if (sw && pop) {
      const open = !pop.classList.contains('open')
      pop.classList.toggle('open',open)
      root.querySelectorAll('.switcher').forEach(b => b.setAttribute('aria-expanded',String(open)))
      // Review #1: kit.css's own top:120px is only a closed-state fallback --
      // it covered the switcher that opened it, since the popup never knew
      // which one that was. On desktop, anchor it under the actual button
      // just clicked (sw1 in the sidebar, sw2 on the phone top bar never hits
      // this branch's layout, since kit.css repositions .menu-pop for phone).
      if (open && window.innerWidth > 760) {
        const r = sw.getBoundingClientRect()
        pop.style.top = (r.bottom + 4) + 'px'
      } else if (!open) {
        pop.style.top = ''
      }
    } else if (pop && !e.target.closest('#evpop')) {
      pop.classList.remove('open')
      root.querySelectorAll('.switcher').forEach(b => b.setAttribute('aria-expanded','false'))
    }
    if (e.target.closest('[data-menu]')) { e.preventDefault(); app.classList.toggle('menu-open') }
    else if (!e.target.closest('.side')) app.classList.remove('menu-open')
    if (e.target.closest('[aria-disabled="true"]')) { e.preventDefault(); e.stopPropagation() }
  })
  // A7/A16: on a bright event colour (the mock-judging yellow), white ink is
  // unreadable -- ui.markInk picks white or the kit's own dark ink by the
  // mark's luminance instead of always white.
  root.querySelectorAll('.ev-mark').forEach(mark => {
    const entry = events.find(e => initials(e.title) === mark.textContent)
    const color = entry?.color || event?.color
    if (/^#[0-9a-f]{6}$/i.test(color || '')) {
      mark.style.setProperty('--ev',color)
      mark.style.color = markInk(color)
    }
  })
  // A5: the status bar already repeats the stage (pill plus track) and the
  // deadline, so the join/judge links only need to live here outside
  // Overview, where the Share cards already carry them.
  if (role === 'organiser' && section !== 'overview') {
    for (const [key,label] of [['join_url','Copy join link'],['judge_url','Copy judge link']]) {
      if (detail.organiser?.[key]) root.querySelector('.status').append(copyButton(detail.organiser[key],label))
    }
  }
  return root.querySelector('main')
}
