import { eventOrder, eventGroups } from './event-list.js'
export { eventOrder } from './event-list.js'
import { nav, href } from './nav.js'
import { esc, stageLabel, roleLabel, initials, nextDeadline, dateTime, copyButton, markInk } from './ui.js'
import { needCount, newsCount, unreadAnnouncements } from './needs.js'
  // Rail rule (atlas 2026-10-30): no two sidebar icons may look alike, since
  // the collapsed rail has only icons. Overview is now its own dashboard
  // glyph (Home keeps the house), Teams three heads (People two), Account a
  // person in a circle -- Teams and Account used to share a single-person
  // glyph with each other and with Team/User having the same path.
  const I = {
    home:'M3 10.5 10 4l7 6.5V17a1 1 0 0 1-1 1h-3.5v-5h-5v5H4a1 1 0 0 1-1-1z',
    overview:'M3 3h6v7H3zM11 3h6v4h-6zM11 9h6v8h-6zM3 12h6v5H3z',
    calendar:'M4 5h12a1 1 0 0 1 1 1v10a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V6a1 1 0 0 1 1-1zm-1 4h14M7 3v4m6-4v4',
    megaphone:'M3 8v4h3l6 4V4L6 8zm12-1.5a4 4 0 0 1 0 7',
    users:'M7.5 9a3 3 0 1 0 0-6 3 3 0 0 0 0 6zM2 17c0-3 2.5-5 5.5-5s5.5 2 5.5 5m1-8a2.5 2.5 0 1 0 0-5m1.5 8c2 .4 3.5 2.2 3.5 5',
    team:'M10 8a2.4 2.4 0 1 0 0-4.8A2.4 2.4 0 0 0 10 8zM4.6 10a1.9 1.9 0 1 0 0-3.8 1.9 1.9 0 0 0 0 3.8zM15.4 10a1.9 1.9 0 1 0 0-3.8 1.9 1.9 0 0 0 0 3.8zM6.2 16.5c0-2.4 1.7-4.3 3.8-4.3s3.8 1.9 3.8 4.3M1.8 15c0-1.7 1.2-2.9 2.8-2.9M18.2 15c0-1.7-1.2-2.9-2.8-2.9',
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
    user:'M10 18a8 8 0 1 0 0-16 8 8 0 0 0 0 16zM10 10.2a2.6 2.6 0 1 0 0-5.2 2.6 2.6 0 0 0 0 5.2zM5.3 15.6c.9-1.9 2.6-3 4.7-3s3.8 1.1 4.7 3',
    signout:'M8 4H4v12h4M12 6l4 4-4 4M16 10H8',
    help:'M10 18a8 8 0 1 0 0-16 8 8 0 0 0 0 16zM8 7.5a2 2 0 1 1 2.8 1.8c-.5.3-.8.7-.8 1.2V11m0 3h.01',
    events:'M3 5h14M3 10h14M3 15h14',
    chev:'M6 8l4 4 4-4',
    collapse:'M3 4h14v12H3zM8 4v12M14 8l-2 2 2 2',
    expand:'M3 4h14v12H3zM8 4v12M12 8l2 2-2 2'
  };
  export function ico(n,cls){ return '<svg class="'+(cls||'ico')+'" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="'+(I[n]||I.file)+'"/></svg>'; }

// Close the phone event dropdown when the viewport changes.
let resizeBound = false
function bindResizeClose() {
  if (resizeBound) return
  resizeBound = true
  window.addEventListener('resize', () => {
    const pop = document.getElementById('evpop')
    if (!pop || !pop.classList.contains('open')) return
    pop.classList.remove('open')
    document.querySelectorAll('.switcher').forEach(b => b.setAttribute('aria-expanded', 'false'))
  })
}
// Collapsible sidebar (atlas 2026-10-30, desktop only, 761px+): the panel
// button beside the brand, or the `[` key outside a text field, folds the
// 248px sidebar into a 56px icon rail and back. Remembered per browser
// (localStorage 'sh_side'; every read/write in try/catch, default
// expanded) and applied before the first paint, so there's no flash --
// readSideCollapsed() is called while building the very first innerHTML
// below, not after.
function readSideCollapsed() {
  try { return localStorage.getItem('sh_side') === 'collapsed' } catch (e) { return false }
}
function writeSideCollapsed(collapsed) {
  try { localStorage.setItem('sh_side', collapsed ? 'collapsed' : 'expanded') } catch (e) {}
}
let sideKeyBound = false
function bindSideKeyOnce() {
  if (sideKeyBound) return
  sideKeyBound = true
  document.addEventListener('keydown', e => {
    if (e.key !== '[' || e.metaKey || e.ctrlKey || e.altKey) return
    const t = e.target
    if (t && (t.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(t.tagName))) return
    // R21: a dialog (ui.dialog's own scrim) traps focus on one of its own
    // buttons -- that counts as "outside a text field", so the shortcut
    // used to fire straight through it and collapse the sidebar behind an
    // open modal.
    if (document.querySelector('.scrim')) return
    // A plain viewport-width check, same as the popup positioning below --
    // the server's own theme test bans the other common way to ask this
    // (a media-query API call) project-wide as a sign of a page deciding
    // its own theme.
    if (window.innerWidth <= 760) return
    const app = document.querySelector('.app')
    const toggle = document.querySelector('[data-side-toggle]')
    if (app && toggle) toggle.click()
  })
}
// supportLink is the Help menu's "Contact support" link. The server never
// substitutes anything inside a <script> element (see chrome.go's
// rewriteSupportAddress), so this install's own contact reaches the app
// through data-support-contact on <body> (set on hack/app.html, rewritten
// server-side for a self-hosted install) instead of a literal mailto: here.
// With no address configured -- a self-hosted install that set neither
// SUPPORT_EMAIL nor IDLE_REPLY_TO -- the link is left off rather than shown
// with nowhere to go.
function supportLink() {
  const contact = (document.body.getAttribute('data-support-contact') || '').trim()
  return contact.indexOf('@') !== -1 ? `<a href="mailto:${esc(contact)}">Contact support</a>` : ''
}
export function mount(ctx, section, screenId) {
  bindResizeClose()
  bindSideKeyOnce()
  const { event: detail, role, me } = ctx
  let events = ctx.events
  const event = detail?.event
  const items = (nav[role] || []).filter(it => !it.stages || (event?.results_published && it.stages.includes(event.stage === 'archived' ? 'ended' : event?.stage)))
  // Teams redesign (2026-10-31): nav.js's own item objects carry no live
  // counts (they never did -- `it.badge` was always undefined), so the two
  // real badges this plan asks for are computed here, from the same event
  // view every screen already reads. Participant Team: pending join
  // requests, admins only (members never see a number they can't act on).
  // Organiser Teams: people not on a team, Open through Building only (the
  // stage Teams itself disables Fill teams/placement past).
  //
  // What waits on the person (atlas 2026-11-02): the server's needs_you
  // items drive the rest. Participant Team: pending requests (admins) plus
  // invites. Participant Overview: unseen news lines. Participant
  // Announcements: posted since this browser last opened that screen
  // (client-only, needs.js). Organiser People: applications waiting for a
  // decision (nav.js's own stage_rule for People).
  const needs = detail?.needs_you || []
  function badgeFor(it) {
    if (role === 'participant' && it.id === 'team') return needCount(needs, ['join_request', 'invite']) || (detail?.me?.team?.admin && detail.me.team.pending_requests) || 0
    if (role === 'participant' && it.id === 'home') return newsCount(needs)
    if (role === 'participant' && it.id === 'announcements') return section === 'announcements' ? 0 : unreadAnnouncements(event?.slug, event?.announcement_times)
    if (role === 'organiser' && it.id === 'people') return needCount(needs, ['application_new'])
    if (role === 'organiser' && it.id === 'teams' && ['open','building'].includes(event?.stage)) return detail?.organiser?.counts?.on_no_team || 0
    return 0
  }
  function tip(label, badge) { return label + (badge ? ' · ' + badge : '') }
  function link(it) {
    const disabled = role === 'participant' && ['entry','website'].includes(it.id) && !detail?.me?.team
    const badge = badgeFor(it)
    return `<a href="${esc(href(role,event.slug,it.id))}"${section === it.id ? ' aria-current="page"' : ''}${disabled ? ' class="later" aria-disabled="true" title="Join or start a team first"' : ''} data-tip="${esc(tip(it.label, badge))}">${ico(it.icon)}<span>${esc(it.label)}</span>${badge ? `<span class="count">${badge}</span>` : ''}</a>`
  }
  // Top-left brand: the one thing that always leads Home (/events), from any
  // screen (owner decision 2026-10-30). Replaces the switcher's old top-left
  // spot -- the switcher now heads the event section below instead, and only
  // when an event is actually open.
  // Sign out sits in the sidebar foot next to Account (ux-arbiter 2026-10-30).
  const signedIn = !!ctx.api.apiKey()
  const personName = me?.display_name || me?.login || me?.username || me?.email || ''
  function brand() {
    // mark-short (collapsed rail) is aria-hidden; the sr-only span carries
    // "Home" for a screen reader once mark-full is display:none, same as
    // the prototype's own brand().
    return `<a class="brand" href="/events" data-tip="Home"><span class="mark"><span class="mark-full">simple<b>·</b>hack</span><span class="mark-short" aria-hidden="true">s<b>·</b>h</span><span class="sr-only"> Home</span></span>${personName ? `<span class="person">${esc(personName)}</span>` : ''}</a>`
  }
  function toggleBtn(collapsed) {
    const label = collapsed ? 'Expand sidebar' : 'Collapse sidebar'
    return `<button class="side-toggle" type="button" data-side-toggle aria-controls="side" aria-expanded="${!collapsed}" aria-label="${label}" data-tip="${label}  [">${ico(collapsed ? 'expand' : 'collapse')}</button>`
  }
  const topNav = `<nav class="top-nav" aria-label="Home"><a href="/events"${screenId === 'landing-your-events' ? ' aria-current="page"' : ''} data-tip="Your events">${ico('home')}<span>Your events</span></a><a href="/directory" data-tip="Find a hackathon">${ico('globe')}<span>Find a hackathon</span></a></nav>`
  function switcher(id) {
    return `<button class="switcher" id="${id}" aria-expanded="false" aria-controls="evpop" data-tip="${esc(tip(event?.title || 'Simple Hack', roleLabel(detail?.role || role)))}"><span class="ev-mark" data-event-slug="${esc(event?.slug || '')}">${esc(initials(event?.title || 'Simple Hack'))}</span><span><span class="ev-name">${esc(event?.title || 'Simple Hack')}</span><span class="ev-role">${esc(roleLabel(detail?.role || role))}</span></span>${ico('chev','chev')}</button>`
  }
  // Owner decision 2026-10-30: the dropdown only lists events now -- Home and
  // Find a hackathon already live in the sidebar's always-shown top section,
  // so the old "More" block here just repeated them.
  function sections() {
    let group = null
    return items.map(it => {
      const label = it.group && it.group !== group ? `<p class="nav-group" data-nav-group>${esc(it.group)}</p>` : ''
      group = it.group
      return label + link(it)
    }).join('') + (role === 'judge' && detail?.can?.manage ? `<a href="${esc(href('organiser', event.slug, 'overview'))}" data-tip="Manage event">${ico('settings')}<span>Manage event</span></a>` : '')
  }
  function eventRow(e, expanded) {
    const label = roleLabel(e.role) + ' · ' + (e.taken_down ? 'Taken down' : e.approval_status === 'pending' ? 'Waiting for approval' : e.approval_status === 'rejected' ? 'Not accepted' : stageLabel(e.stage))
    const attrs = `class="pane-event" data-tip="${esc(e.title + ' · ' + label)}" aria-label="${esc(e.title + ' · ' + label)}" title="${esc(e.title + ' · ' + label)}"`
    const content = `<span class="ev-mark" data-event-slug="${esc(e.slug)}">${esc(initials(e.title))}</span><span class="event-label"><b>${esc(e.title)}</b><small>${esc(label)}</small></span>`
    const row = e.taken_down && e.role !== 'organiser' ? `<div ${attrs}>${content}</div>` : `<a ${attrs} href="${esc(href(e.role, e.slug, e.role === 'organiser' ? 'overview' : 'home'))}">${content}</a>`
    return row + (expanded && e.slug === event?.slug ? `<div class="event-sections">${sections()}</div>` : '')
  }
  function eventList(expanded) {
    const { current, past, pastCount } = eventGroups(events, event?.slug)
    const inspection = expanded && detail?.admin_view ? `<div class="inspection-context" aria-label="Current event inspection">${eventRow({ ...event, role: 'organiser' }, false)}<small>Read-only inspection</small><div class="event-sections">${sections()}</div></div>` : ''
    return inspection + current.slice(0, 5).map(e => eventRow(e, expanded)).join('')
      + `<a href="/events" data-tip="All events (${events.length})">${ico('events')}<span>All events (${events.length})</span></a>`
      + (pastCount ? `<details class="past-events"><summary>Past events (${pastCount})</summary>${past.slice(0, 5).map(e => eventRow(e, false)).join('')}<a href="/events?past=1">All past events</a></details>` : '')
  }
  const popup = `<div class="menu-pop" id="evpop">${eventList(false)}</div>`

  const tabs = items.filter(it => it.phone_tab)
  // A18: the tab bar's own More link and the top "Menu" button both opened
  // the same sheet. Keep one control: More always sits in the tab bar
  // (Account and Help live inside its sheet); kit.css hides the top button
  // whenever a tab bar is present.
  const tabbar = role && role !== 'organiser' ? `<nav class="tabbar" aria-label="Sections">${tabs.map(it => { const badge = badgeFor(it); return `<a href="${esc(href(role,event.slug,it.id))}"${section === it.id ? ' aria-current="page"' : ''}>${ico(it.icon)}<span>${esc(it.short || it.label)}</span>${badge ? `<span class="count">${badge}</span>` : ''}</a>` }).join('')}<a href="#" data-menu>${ico('more')}<span>More</span></a></nav>` : ''
  // On phone, inside an event the top bar shows the switcher (context + a
  // quick jump to another event) instead of the brand; outside an event
  // (Home, Account, Create event) there is nothing to switch, so it shows
  // the brand instead -- still one tap from Menu either way.
  const phoneTop = event ? switcher('sw2') : brand()
  // S14: once an event has ended, the status bar shows only "Ended" (or,
  // for a judge, "Scoring locked") -- never a leftover "Starts …"/deadline
  // line. nextDeadline only looks at future timestamps, so a stray future
  // starts_at on a test/odd event (ended before it was due to start) could
  // otherwise still surface here.
  const next = event && event.stage !== 'archived' ? nextDeadline(event) : null
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
  // Owner decision 2026-10-31 (overrides the 2026-10-30 judge-only-pill
  // rule): every role sees the same stage pill, in the same words, on every
  // screen -- a judge's pill used to replace the stage entirely with
  // "Scoring open/locked/not open", which is gone now that the stage pill
  // always reads the real stage. Judges keep a second pill for scoring,
  // right after the stage pill, dropped once the event has ended (there is
  // nothing left to score, and the stage pill already says "Ended").
  const judgeScoringPill = role === 'judge' && event && event.stage !== 'archived'
    ? `<span class="pill ${event.judging_locked_at ? 'warn' : event.scoring_open ? 'ok' : ''}"><span class="d"></span>${event.judging_locked_at ? 'Scoring locked' : event.scoring_open ? 'Scoring open' : 'Scoring not open'}</span>`
    : ''
  // The full done/now/upcoming track, read-only, now shows on every role's
  // own home screen (organiser Overview, participant/judge Home, nav_id
  // "home") -- not only the organiser's, per the same decision.
  const showTrack = !!event && ((role === 'organiser' && section === 'overview') || ((role === 'participant' || role === 'judge') && section === 'home'))
  const status = event ? `<div class="status"><span class="pill stage"><span class="d"></span>${esc(stageLabel(event.stage))}</span>${judgeScoringPill}${next ? `<span class="muted">${esc(next[0])} · ${esc(dateTime(next[1], event, next[2] ? { dateOnly: true } : {}))}</span>` : ''}<span class="grow"></span>${showTrack ? `<div class="stages">${track.map((s,i) => `<span class="${s === event.stage ? 'now' : i < track.indexOf(event.stage) ? 'done' : ''}">${esc(stageLabel(s))}</span>`).join('')}</div>` : ''}</div>` : ''
  const root = document.getElementById('app')
  // Two sidebar levels (owner decision 2026-10-30): the top-nav (Home, Find a
  // hackathon) is always shown; the event section (switcher + role nav) only
  // exists while an event is open -- on Home itself (no event) only the top
  // section shows.
  const eventNav = signedIn ? `<div class="event-nav"><nav class="nav" aria-label="Your events">${eventList(true)}</nav></div>` : ''
  let collapsed = readSideCollapsed()
  root.innerHTML = `<div class="app${tabbar ? ' has-tabs' : ''}${collapsed ? ' side-collapsed' : ''}"><aside class="side" id="side" aria-label="Navigation"><div class="side-top">${brand()}${toggleBtn(collapsed)}</div>${topNav}${eventNav}<div class="side-foot"><a href="/account" data-tip="Account">${ico('user')}<span>Account</span></a>${signedIn ? `<a href="/" data-signout data-tip="Sign out">${ico('signout')}<span>Sign out</span></a>` : ''}<details><summary data-tour="help" data-tip="Help">${ico('help')}<span>Help</span></summary><a href="/get-started">Get started</a>${walkthroughHref ? `<a href="${esc(walkthroughHref)}">Replay walkthrough</a>` : ''}${supportLink()}</details></div></aside>${event ? popup : ''}<div class="col"><div class="phone-top">${phoneTop}<button class="menu-btn" data-menu>Menu</button></div>${status}<div class="content"><main class="page"></main></div></div>${tabbar}</div>`
  const app = root.firstElementChild
  // On Home itself (no event) there is no switcher and no popup to toggle --
  // pop is null there, so every branch below that touches it is guarded.
  const pop = root.querySelector('#evpop')
  function setSide(c) {
    collapsed = c
    app.classList.toggle('side-collapsed', c)
    const old = app.querySelector('[data-side-toggle]')
    if (old) old.outerHTML = toggleBtn(c)
    writeSideCollapsed(c)
    const fresh = app.querySelector('[data-side-toggle]')
    if (fresh) fresh.focus()
    if (pop) { pop.classList.remove('open'); root.querySelectorAll('.switcher').forEach(b => b.setAttribute('aria-expanded', 'false')) }
  }
  let menuOpener = null
  app.addEventListener('click', e => {
    if (e.target.closest('[data-side-toggle]')) { e.preventDefault(); setSide(!collapsed); return }
    const sw = e.target.closest('.switcher')
    if (sw && pop) {
      const open = !pop.classList.contains('open')
      pop.classList.toggle('open',open)
      root.querySelectorAll('.switcher').forEach(b => b.setAttribute('aria-expanded',String(open)))
    } else if (pop && !e.target.closest('#evpop')) {
      pop.classList.remove('open')
      root.querySelectorAll('.switcher').forEach(b => b.setAttribute('aria-expanded','false'))
    }
    if (e.target.closest('[data-signout]')) { e.preventDefault(); ctx.api.signOut(ctx.ui.confirm); return }
    if (e.target.closest('[data-menu]')) { e.preventDefault(); menuOpener = e.target.closest('[data-menu]'); app.classList.toggle('menu-open'); if (app.classList.contains('menu-open')) app.querySelector('.side a')?.focus(); else menuOpener.focus() }
    else if (e.target.closest('.side a')) { app.classList.remove('menu-open'); menuOpener?.focus() }
    else if (!e.target.closest('.side')) app.classList.remove('menu-open')
    if (e.target.closest('[aria-disabled="true"]')) { e.preventDefault(); e.stopPropagation() }
  })
  // A7/A16: on a bright event colour (the mock-judging yellow), white ink is
  // unreadable -- ui.markInk picks white or the kit's own dark ink by the
  // mark's luminance instead of always white.
  root.querySelectorAll('.ev-mark').forEach(mark => {
    const entry = events.find(e => e.slug === mark.dataset.eventSlug)
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
  ctx.updateEvents = fresh => {
    events = fresh
    const pane = root.querySelector('.event-nav > .nav')
    if (pane) pane.innerHTML = eventList(true)
    if (pop) pop.innerHTML = eventList(false)
    root.querySelectorAll('.pane-event .ev-mark').forEach(mark => {
      const entry = events.find(e => e.slug === mark.dataset.eventSlug)
      if (/^#[0-9a-f]{6}$/i.test(entry?.color || '')) { mark.style.setProperty('--ev', entry.color); mark.style.color = markInk(entry.color) }
    })
  }
  return root.querySelector('main')
}
