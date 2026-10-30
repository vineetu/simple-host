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
export function mount(ctx, section) {
  const { event: detail, role, events } = ctx
  const event = detail?.event
  const items = (nav[role] || []).filter(it => !it.stages || (event?.results_published && it.stages.includes(event.stage === 'archived' ? 'ended' : event?.stage)))
  function link(it) {
    const disabled = role === 'participant' && ['entry','website'].includes(it.id) && !detail?.me?.team
    return `<a href="${esc(href(role,event.slug,it.id))}"${section === it.id ? ' aria-current="page"' : ''}${disabled ? ' class="later" aria-disabled="true" title="Join or start a team first"' : ''}>${ico(it.icon)}<span>${esc(it.label)}</span></a>`
  }
  function switcher(id) {
    return `<button class="switcher" id="${id}" aria-expanded="false" aria-controls="evpop"><span class="ev-mark">${esc(initials(event?.title || 'Simple Hack'))}</span><span><span class="ev-name">${esc(event?.title || 'Simple Hack')}</span><span class="ev-role">${esc(roleLabel(role) || 'Your events')}</span></span>${ico('chev','chev')}</button>`
  }
  const popup = `<div class="menu-pop" id="evpop"><div class="lbl">Your events</div>${eventOrder(events).map(e => `<a href="${esc(href(e.role,e.slug))}"><span class="ev-mark">${esc(initials(e.title))}</span><span>${esc(e.title)}<small>${esc(roleLabel(e.role))} · ${esc(e.taken_down ? 'Taken down' : stageLabel(e.stage))}</small></span></a>`).join('')}<div class="lbl">More</div><a href="/events">${ico('events')}<span>All your events</span></a><a href="/directory">${ico('globe')}<span>Find a hackathon</span></a></div>`
  const tabs = items.filter(it => it.phone_tab)
  // A18: the tab bar's own More link and the top "Menu" button both opened
  // the same sheet. Keep one control: More always sits in the tab bar
  // (Account and Help live inside its sheet); kit.css hides the top button
  // whenever a tab bar is present.
  const tabbar = role && role !== 'organiser' ? `<nav class="tabbar" aria-label="Sections">${tabs.map(it => `<a href="${esc(href(role,event.slug,it.id))}"${section === it.id ? ' aria-current="page"' : ''}>${ico(it.icon)}<span>${esc(it.short || it.label)}</span></a>`).join('')}<a href="#" data-menu>${ico('more')}<span>More</span></a></nav>` : ''
  const next = nextDeadline(event)
  const track = ['draft','open','building','closed','judging','results','archived']
  // replay-organiser-walkthrough / replay-judging-walkthrough: Help always
  // offers a way back into the role's walkthrough panel, even once it's
  // been dismissed, by deep-linking to the screen that owns it.
  const walkthroughHref = event && (role === 'organiser' || role === 'judge')
    ? href(role,event.slug,role === 'organiser' ? 'overview' : 'home') + '&walkthrough=1'
    : ''
  // The judge pill has its own wording while scoring isn't open yet --
  // "Not open yet" is also the Draft stage's own label (A21/shell.js#2), so
  // judges get a phrase that can't be confused with the event's stage.
  const status = event ? `<div class="status"><span class="pill stage"><span class="d"></span>${esc(role === 'judge' ? (event.judging_locked_at ? 'Scoring locked' : event.scoring_open ? 'Scoring open' : 'Scoring not open') : stageLabel(event.stage))}</span>${next ? `<span class="muted">${esc(next[0])} · ${esc(dateTime(next[1],event))}</span>` : ''}<span class="grow"></span>${role === 'organiser' && section === 'overview' ? `<div class="stages">${track.map((s,i) => `<span class="${s === event.stage ? 'now' : i < track.indexOf(event.stage) ? 'done' : ''}">${esc(stageLabel(s))}</span>`).join('')}</div>` : ''}</div>` : ''
  const root = document.getElementById('app')
  root.innerHTML = `<div class="app${tabbar ? ' has-tabs' : ''}"><aside class="side" aria-label="Event navigation">${switcher('sw1')}<nav class="nav">${items.map(link).join('')}${!event ? `<a href="/events">${ico('events')}<span>Your events</span></a>` : ''}</nav><div class="side-foot"><a href="/account">${ico('user')}<span>Account</span></a><details><summary data-tour="help">${ico('help')}<span>Help</span></summary><a href="/get-started">Get started</a>${walkthroughHref ? `<a href="${esc(walkthroughHref)}">Replay walkthrough</a>` : ''}<a href="mailto:support@simple-host.app">Contact support</a></details></div></aside>${popup}<div class="col"><div class="phone-top">${switcher('sw2')}<button class="menu-btn" data-menu>Menu</button></div>${status}<div class="content"><main class="page"></main></div></div>${tabbar}</div>`
  const app = root.firstElementChild
  const pop = root.querySelector('#evpop')
  app.addEventListener('click', e => {
    const sw = e.target.closest('.switcher')
    if (sw) {
      const open = !pop.classList.contains('open')
      pop.classList.toggle('open',open)
      root.querySelectorAll('.switcher').forEach(b => b.setAttribute('aria-expanded',String(open)))
    } else if (!e.target.closest('#evpop')) {
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
