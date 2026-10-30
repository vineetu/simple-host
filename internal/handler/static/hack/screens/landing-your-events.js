import { href } from '../nav.js'
import { eventOrder } from '../shell.js'

export function title() { return 'Home' }

// Home (owner decision 2026-10-30): the person's own home, one level above
// any event -- the top-left brand link always leads here. Was the full-page
// version of the switcher dropdown (then titled "Your events"); now a real
// page with a plain next action per row and the other starting points
// (Find a hackathon, Create event, Account) alongside the list.
export async function render(ctx) {
  const { main, events, ui } = ctx
  // A2: the "Leave this event" menu advertised a feature that doesn't
  // exist yet (SIM-340) -- removed rather than shown disabled with an
  // internal ticket id.
  // Part B #2: one link per row, not a title link plus a separate "next"
  // link plus two pills.
  // nextAction: a plain next step in the person's own words, built only
  // from role + stage (the two fields the events list actually returns) --
  // never a count or a date the list doesn't provide. Review #13: three of
  // these guessed wrong from stage alone, because the list has no
  // results_published and no "is this person on a team" signal --
  // 'Publish results' (organiser) and 'See results' (archived) both assert
  // a publish that may never have happened, and 'Find a team' (participant,
  // open) is told to someone who may already be on one, or waiting on a
  // request. Neutral copy ("Open results", "Open event") replaces the guess
  // in exactly those cases; the real screen, with the real event detail,
  // still shows the precise state once they open it.
  function nextAction(e) {
    if (e.taken_down) return e.role === 'organiser' ? 'Overview' : ''
    if (e.role === 'organiser') {
      return ({
        draft: 'Finish setting up', open: 'Sign-up is open', building: 'Teams are building',
        closed: 'Set up judging', judging: 'Judges are scoring', results: 'Open results'
      })[e.stage] || 'Overview'
    }
    if (e.role === 'judge') {
      return ({
        draft: 'Not open yet', open: 'Not open yet', building: 'Not open yet',
        closed: 'Score teams', judging: 'Score teams',
        results: 'See results', archived: 'Open results', ended: 'Open results'
      })[e.stage] || 'Open'
    }
    // participant
    return ({
      draft: 'Not open yet', open: 'Open event', building: 'Keep building',
      closed: 'Entries closed', judging: 'Judges are scoring',
      results: 'See results', archived: 'Open results', ended: 'Open results'
    })[e.stage] || 'Open'
  }
  const row = e => {
    const section = e.role === 'judge' && ['closed','judging'].includes(e.stage) ? 'queue' : e.role === 'participant' && e.stage === 'results' ? 'results' : undefined
    const blocked = e.taken_down && e.role !== 'organiser'
    const tag = blocked ? 'div' : 'a'
    const hrefAttr = blocked ? '' : ` href="${ui.esc(href(e.role,e.slug,section))}"`
    return `<${tag} class="r"${hrefAttr}><span class="ev-mark">${ui.esc(ui.initials(e.title))}</span><span class="main"><b>${ui.esc(e.title)}</b><span>${ui.esc(ui.roleLabel(e.role))} · ${ui.esc(e.taken_down ? 'Taken down' : ui.stageLabel(e.stage))}</span></span>${blocked ? '' : `<span class="meta">${ui.esc(nextAction(e))}</span>`}</${tag}>`
  }
  const attention = e => !e.taken_down && ((e.role === 'organiser' && !['archived','ended'].includes(e.stage)) || (e.role === 'judge' && ['closed','judging'].includes(e.stage)))
  const list = eventOrder(events)
  const quickLinks = `<div class="row-flex"><a class="btn" href="/directory">Find a hackathon</a><a class="btn quiet" href="/account">Account</a></div>`
  main.innerHTML = `<div class="ph"><div class="t"><h1>Home</h1></div>${list.length ? `<a class="btn primary" href="/events/new">Create event</a>` : ''}</div>` + (!list.length ? `<div class="locked"><h2>You’re not part of any event yet</h2><p><a class="btn primary" href="/events/new">Create event</a></p><p><a class="btn" href="/directory">Find a hackathon</a></p><p>Have a join or judge link? Open it to get started.</p></div><div class="row-flex"><a class="btn quiet" href="/account">Account</a></div>` : [true,false].map(needed => {
    const group = list.filter(e => attention(e) === needed)
    return group.length ? `<p class="section-title">${needed ? 'Needs attention' : 'Recent'}</p><div class="list">${group.map(row).join('')}</div>` : ''
  }).join('') + quickLinks)
  main.querySelectorAll('.ev-mark').forEach((mark,i) => {
    const color = list[i].color
    if (/^#[0-9a-f]{6}$/i.test(color || '')) { mark.style.setProperty('--ev',color); mark.style.color = ui.markInk(color) }
  })
}
