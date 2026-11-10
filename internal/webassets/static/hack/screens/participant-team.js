import { errText, isBlockedCode } from './_partB-util.js'
import { startTour } from '../tour.js'
import { row, rowMenuHTML, wireRowMenus, teamLink, COPY_ICON, LOCK_ICON } from './_team-util.js'
import { messagesFromOrganisersCardHTML } from './_team-message-dialog.js'

// Teams redesign (owner, 2026-10-31): the old side-by-side Start/Join cards
// and the single long on-team page are gone. No team: one question, "How
// do you want to team up?", three choice cards, and only the chosen way's
// panel underneath. On a team: Members / Find people / Settings sub-pages,
// like the Event page (spec.json participant-team.sub_pages).
const TABS = ['members', 'people', 'settings']
const TAB_LABEL = { members: 'Members', people: 'Find people', settings: 'Settings' }

export function title(ctx) { return 'Team' }

// Guided walkthrough: shown once, automatically, the first time a
// participant with no team opens Team while teams are open. Guarded so a
// refresh of this same screen doesn't keep popping it back open within one
// page load.
let autoTourTried = false

// "Just filled" (spec state) has no server signal: there is no field that
// says "an organiser's Fill teams placed you here", and no Go change is in
// scope for this batch. participant-home.js approximates it the same way,
// under the same 'sh-team-baseline-<user>-<event>' key on purpose -- "you
// are on this team now" is one fact, so whichever of Home/Team the person
// sees first updates the shared baseline, instead of showing the same
// banner twice on two screens. markSelfJoin() marks a self-service join
// right before the refresh those actions already do, so a join/accept the
// person themselves triggered never reads back as "placed by the
// organiser".
function justPlacedNote(uid, slug, team) {
  const selfKey = 'sh-selfjoin-' + slug
  let self = false
  try { self = sessionStorage.getItem(selfKey) === '1'; sessionStorage.removeItem(selfKey) } catch (e) {}
  const baselineKey = 'sh-team-baseline-' + uid + '-' + slug
  const teamSlug = team ? team.slug : ''
  let baseline = null
  try { baseline = localStorage.getItem(baselineKey) } catch (e) {}
  try { localStorage.setItem(baselineKey, teamSlug) } catch (e) {}
  if (baseline === null) return false
  return baseline !== teamSlug && !!teamSlug && !self
}
function markSelfJoin(slug) { try { sessionStorage.setItem('sh-selfjoin-' + slug, '1') } catch (e) {} }

const WAY_COPY = {
  ask: ['Ask to join a team', "Pick a team that's looking for people. Its admins say yes or no."],
  code: ['Join with a link or code', 'A teammate sent you their team link or code. You join straight away.'],
  start: ['Start a new team', "You'll be its admin. Share its link to bring people in."]
}

export async function render(ctx) {
  const { main, api: client, ui, event: detail, refresh, params } = ctx
  const api = client.api
  const esc = ui.esc
  const ev = detail.event
  const me = detail.me
  const team = me.team

  main.innerHTML = '<div class="ph"><div class="t"><h1>Team</h1><p>' + esc(ev.title || '') + '</p></div></div><p class="note">Loading…</p>'

  wireRowMenus()

  const deadlinePassed = !!(ev.submission_deadline && new Date(ev.submission_deadline) <= new Date())
  const teamsOpen = (ev.stage === 'open' || ev.stage === 'building') && !ev.taken_down
  const ended = ev.stage === 'archived'
  const frozen = !!ev.taken_down || (team ? !!team.frozen : ev.stage === 'closed')
  const canStart = teamsOpen && !frozen && !deadlinePassed
  const canJoinByCode = teamsOpen && !ended
  const canDiscover = teamsOpen && !deadlinePassed
  const isAdmin = !!(team && team.admin)
  // Removal notice (audit 2026-11-02): someone taken off a team lands here
  // next, so the reason shows on Team too, not only on Overview.
  const removalNote = !team && me.team_removal_notice ? `<div class="note bad">${esc(me.team_removal_notice)}</div>` : ''
  // canAct: on-team writes in general (not frozen/ended); admin-only
  // actions (rename, looking-for, lock, new link, requests, remove,
  // admin toggles) additionally require isAdmin. Invites stay open to
  // every member (decision 6), gated by canInvite below instead.
  const canAct = !!team && !frozen
  const canInvite = canAct && !team.locked
  const uid = (ctx.me && (ctx.me.id || ctx.me.username)) || 'anon'
  const justPlaced = justPlacedNote(uid, ev.slug, team)

  let finding = { enabled: false, teams: [], solo: [], pending: [], looking: false, intro: '' }
  try {
    const r = await api('GET', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/teams/open')
    if (r.ok) finding = Object.assign(finding, r.data)
  } catch (e) { if (client.isSignedOut(e)) return }
  if (!ctx.alive()) return

  // Messages from the organisers (notices contract 2026-11-05): only on a
  // team -- the route is "own current team", empty with no team at all.
  let messages = []
  if (team) {
    try {
      const mr = await api('GET', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/team/messages')
      if (mr.ok) messages = mr.data.messages || []
    } catch (e) { if (client.isSignedOut(e)) return }
    if (!ctx.alive()) return
  }

  const $ = id => main.querySelector('#' + id)

  async function act(btn, fn, { onBlocked, saved } = {}) {
    if (btn) btn.disabled = true
    try {
      const r = await fn()
      if (!r.ok) {
        const code = r.data && r.data.code
        if (isBlockedCode(code) && onBlocked !== false) { ui.toast(errText(r), main, true); await refresh(); return }
        ui.toast(errText(r), btn || main, true)
        if (btn) btn.disabled = false
        return
      }
      if (!team) markSelfJoin(ev.slug)
      if (saved) ui.saved(btn || main)
      await refresh()
    } catch (e) {
      if (client.isSignedOut(e)) return
      ui.toast('Cannot reach the server.', btn || main, true)
      if (btn) btn.disabled = false
    }
  }

  // ----------------------------------------------------------- no team ---
  let way = ''
  // Only called when at least one way in is open -- the caller's own
  // "every way closed" branch (render(), below) replaces the whole screen
  // with the empty-state before this function is ever reached.
  function noTeamHTML() {
    const ways = []
    if (finding.enabled && canDiscover) ways.push('ask')
    if (canJoinByCode) ways.push('code')
    if (canStart) ways.push('start')
    if (!ways.includes(way)) {
      way = !finding.enabled ? (canJoinByCode ? 'code' : 'start')
        : (ways.includes('ask') && (finding.teams.length || finding.pending.some(p => p.kind === 'request'))) ? 'ask'
        : ways.includes('start') ? 'start' : ways[0]
    }
    const invites = finding.enabled ? finding.pending.filter(p => p.kind === 'invite') : []
    const invitesHTML = invites.length
      ? `<h2 class="section-title">Invites for you</h2><div class="list">${invites.map(p => row(esc, p.team_name, 'Invited you to join', `<button type="button" class="btn primary sm" data-accept="${esc(p.id)}">Accept</button><button type="button" class="btn sm" data-decline="${esc(p.id)}">Decline</button>`)).join('')}</div>`
      : ''
    const choices = `<h2 class="section-title">How do you want to team up?</h2><div class="choices stack" role="radiogroup" aria-label="How do you want to team up?" data-tour="team-ways">${ways.map(w => {
      const [t, d] = WAY_COPY[w]
      return `<label class="choice${w === way ? ' on' : ''}"><input type="radio" name="way" value="${w}"${w === way ? ' checked' : ''}><span><b>${esc(t)}</b><span class="d">${esc(d)}</span></span></label>`
    }).join('')}</div>`
    let panel = ''
    if (way === 'ask') panel = askPanelHTML()
    else if (way === 'code') panel = codePanelHTML()
    else if (way === 'start') panel = startPanelHTML()
    return invitesHTML + choices + panel
  }

  function askPanelHTML() {
    const rows = finding.teams.map(t => {
      const req = finding.pending.find(p => p.kind === 'request' && p.team === t.slug)
      const blocked = t.full || !canJoinByCode
      const members = Array.isArray(t.members) ? t.members.filter(Boolean) : []
      const sub = t.member_count + ' of ' + t.team_size_max +
        (t.track && t.track.name ? ' · ' + t.track.name : '') +
        (members.length ? ' · ' + members.join(', ') : '') +
        (t.looking_for && !t.full ? ' · Looking for: ' + t.looking_for : '')
      const btn = req
        ? `<button type="button" class="btn sm" data-withdraw="${esc(req.id)}">Withdraw request</button>`
        : `<button type="button" class="btn sm" data-ask="${esc(t.slug)}"${blocked ? ' disabled' : ''}>${t.full ? 'Full' : 'Ask to join'}</button>`
      return row(esc, t.name, sub, btn)
    }).join('')
    return `<section class="sp" data-tour="open-teams"><div class="sp-head"><h2>Teams looking for people</h2><p>Ask as many as you like. When one says yes, your other requests are withdrawn.</p></div>${finding.teams.length ? `<div class="list">${rows}</div>` : `<div class="empty-state"><h3>No teams looking right now</h3><p>Check back later, or start your own.</p></div>`}</section>` + beFoundHTML()
  }

  function beFoundHTML() {
    return `<section class="card" data-tour="be-found"><h2>Let teams find you</h2><p class="sub">Teams see your name and this line, and can invite you. You choose whether to join.</p><label class="check"><input type="checkbox" id="look-on"${finding.looking ? ' checked' : ''}><span><b>List me as looking for a team</b></span></label><div class="field"><label for="look-intro">A line about you</label><input class="input" id="look-intro" maxlength="120" value="${esc(finding.intro || '')}"><p class="help">Up to 120 characters.</p></div><div class="card-foot"><span class="hint" id="look-hint" hidden>Saved</span><button class="btn" type="button" id="look-save">Save</button></div></section>`
  }

  function codePanelHTML() {
    // location.host, not a literal "simple-hack.app": this example must
    // name this install's own address, not ours.
    return `<section class="card"><h2>Join with a link or code</h2><div class="field"><label for="join-code">Team link or code</label><input class="input" id="join-code" autocomplete="off" spellcheck="false" autocapitalize="off" placeholder="${esc(location.host)}/team/… or k7m2p9qa"${canJoinByCode ? '' : ' disabled'}><p class="help">Paste the whole link or type the 8-character code. Ask a teammate to send it from their Team page.</p><p class="err" id="join-code-err" hidden></p></div><div class="card-foot"><button class="btn primary" type="button" id="join-go"${canJoinByCode ? '' : ' disabled'}>Join team</button></div></section>`
  }

  function startPanelHTML() {
    return `<section class="card"><h2>Start a new team</h2><div class="field"><label for="start-name">Team name</label><input class="input" id="start-name" maxlength="80" autocomplete="off"${canStart ? '' : ' disabled'}><p class="help">You’ll be its admin. You can rename it later.</p><p class="err" id="start-name-err" hidden></p></div><div class="card-foot"><button class="btn primary" type="button" id="start-go"${canStart ? '' : ' disabled'}>Start team</button></div></section>`
  }

  function wireNoTeam() {
    main.querySelectorAll('input[name="way"]').forEach(r => r.addEventListener('change', async () => {
      const next = r.value
      main.querySelectorAll('input[name="way"]').forEach(input => { input.checked = input.value === way })
      if (!await ui.confirmFormLeave()) return
      way = next
      paint()
    }))
    main.querySelectorAll('[data-ask]').forEach(b => b.addEventListener('click', () => act(b, () => api('POST', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/teams/' + encodeURIComponent(b.getAttribute('data-ask')) + '/requests'))))
    main.querySelectorAll('[data-withdraw]').forEach(b => b.addEventListener('click', () => act(b, () => api('DELETE', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/requests/' + encodeURIComponent(b.getAttribute('data-withdraw'))))))
    main.querySelectorAll('[data-accept]').forEach(b => b.addEventListener('click', () => act(b, () => api('POST', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/requests/' + encodeURIComponent(b.getAttribute('data-accept')) + '/accept'))))
    main.querySelectorAll('[data-decline]').forEach(b => b.addEventListener('click', () => act(b, () => api('POST', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/requests/' + encodeURIComponent(b.getAttribute('data-decline')) + '/decline'))))
    const lookOn = $('look-on'), lookSave = $('look-save')
    if (lookSave) lookSave.addEventListener('click', () => act(lookSave, () => api('PUT', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/me/looking', { looking: lookOn.checked, intro: $('look-intro').value }), { saved: true }))
    const joinGo = $('join-go')
    if (joinGo) joinGo.addEventListener('click', () => {
      const err = $('join-code-err')
      const input = $('join-code')
      const code = input.value.trim()
      if (!code) { err.hidden = false; err.textContent = 'Enter a team link or code.'; input.setAttribute('aria-invalid', 'true'); return }
      err.hidden = true; input.removeAttribute('aria-invalid')
      act(joinGo, () => api('POST', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/teams/join', { code }))
    })
    const startGo = $('start-go')
    if (startGo) startGo.addEventListener('click', () => {
      const err = $('start-name-err')
      const input = $('start-name')
      const name = input.value.trim()
      if (!name) { err.hidden = false; err.textContent = 'Enter a team name.'; input.setAttribute('aria-invalid', 'true'); return }
      err.hidden = true; input.removeAttribute('aria-invalid')
      act(startGo, () => api('POST', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/teams', { name }))
    })
  }

  // ----------------------------------------------------------- on team ---
  function resolveTab() {
    const avail = finding.enabled ? TABS : TABS.filter(t => t !== 'people')
    const t = params.get('tab') || ''
    return avail.includes(t) ? t : 'members'
  }

  function subnavHTML(tab, memberCount) {
    const soloCount = finding.enabled ? finding.solo.length : 0
    return `<nav class="subnav" aria-label="Team sections">
<a href="?s=team&tab=members"${tab === 'members' ? ' aria-current="page"' : ''}>Members<span class="n">${memberCount}</span></a>
${finding.enabled ? `<a href="?s=team&tab=people"${tab === 'people' ? ' aria-current="page"' : ''}>Find people${soloCount ? `<span class="n">${soloCount}</span>` : ''}</a>` : ''}
<a href="?s=team&tab=settings"${tab === 'settings' ? ' aria-current="page"' : ''}>Settings</a>
</nav>`
  }

  function teamLinkCardHTML() {
    const { addr, link } = teamLink(team)
    if (frozen) {
      return `<section class="card"><h2>Team link</h2><p class="sub">Entries are closed, so nobody else can join. The code stays here as a record.</p><div class="addr"><span class="mono">${esc(addr)}</span></div></section>`
    }
    if (team.locked) {
      const tail = isAdmin ? `<button class="btn" type="button" id="unlock-team">Unlock team</button>` : `Ask an admin to unlock it.`
      return `<div class="note warn" role="status">${esc(team.name)} is locked. No more people can join, by link, code, request or invite. ${tail}</div>`
    }
    return `<section class="card"><h2>Team link</h2><p class="sub">Anyone in this event who opens it joins ${esc(team.name)} straight away.</p><div class="addr"><span class="mono">${esc(addr)}</span><button class="btn primary" type="button" id="copy-team-link" data-link="${esc(link)}">${COPY_ICON}Copy link</button></div><p class="help" style="margin:10px 0 0">Or share the code <b class="mono">${esc(team.code || '')}</b>.</p></section>`
  }

  function requestsHTML(pendingRequests) {
    if (!pendingRequests.length) return ''
    const rows = pendingRequests.map(p => {
      const act_ = isAdmin
        ? `<button type="button" class="btn primary sm" data-accept="${esc(p.id)}">Accept</button><button type="button" class="btn sm" data-decline="${esc(p.id)}">Decline</button>`
        : `<span class="meta">Waiting for an admin</span>`
      return `<div class="r"><div class="main"><b>${esc(p.display_name)}</b><span>Asked to join</span></div>${act_}</div>`
    }).join('')
    return `<div class="list editable" aria-label="Asked to join"><div class="lh">Asked to join · ${pendingRequests.length}</div>${rows}</div>`
  }

  function memberRowHTML(m, full) {
    const adminPill = m.admin ? '<span class="meta"><span class="pill accent">Admin</span></span>' : ''
    let acts = ''
    if (isAdmin && canAct) {
      if (m.you) {
        acts = `<div class="row-acts">${rowMenuHTML(esc, 'yourself', [{ label: 'Step down as admin', act: 'stepdown' }, { label: 'Leave team', act: 'leave', danger: true }])}</div>`
      } else {
        const items = [m.admin ? { label: 'Remove as admin', act: 'unadmin' } : { label: 'Make admin', act: 'admin' }, { label: 'Remove from team', act: 'remove', danger: true }]
        acts = `<div class="row-acts">${rowMenuHTML(esc, m.display_name || 'this person', items)}</div>`
      }
    }
    if (!isAdmin && canAct && m.you) {
      acts = `<div class="row-acts">${rowMenuHTML(esc, 'yourself', [{ label: 'Leave team', act: 'leave', danger: true }])}</div>`
    }
    return `<div class="r" data-member="${esc(m.user_id || '')}" data-name="${esc(m.display_name || 'this person')}"><div class="main"><b>${esc(m.display_name || 'Someone')}</b><span>${m.you ? 'You' : ''}</span></div>${adminPill}${acts}</div>`
  }

  function membersPanelHTML() {
    const members = team.members || []
    const pendingRequests = finding.pending.filter(p => p.kind === 'request')
    const parts = []
    if (justPlaced) parts.push(`<div class="note ok" role="status">You joined ${esc(team.name)}.</div>`)
    // Round-6 Confusing 2: one clear rule instead of two that contradicted
    // each other -- "Admins decide who joins" sat right above the Team
    // link card's own "Anyone ... joins straight away."
    parts.push(`<div class="sp-head"><h2 id="sp-h" tabindex="-1">Members</h2><p>Anyone with the team link joins straight away. Admins approve requests to join.</p></div>`)
    if (frozen) parts.push(`<div class="note" role="status">Entries are closed, so ${esc(team.name)} can’t change now.</div>`)
    parts.push(messagesFromOrganisersCardHTML(ui, ev, messages))
    parts.push(teamLinkCardHTML())
    if (!frozen) parts.push(requestsHTML(pendingRequests))
    parts.push(`<div class="list editable" aria-label="Members"><div class="lh">Members · ${members.length}</div>${members.map(m => memberRowHTML(m, members.length >= ev.team_size_max)).join('')}</div>`)
    const foot = members.length + ' of ' + ev.team_size_max + ' people.' + (isAdmin && canAct && !team.locked ? ' Lock the team in Settings when it’s complete.' : '')
    parts.push(`<p class="list-foot">${foot}</p>`)
    return parts.join('')
  }

  function peoplePanelHTML() {
    const full = (team.members || []).length >= ev.team_size_max
    const rows = finding.solo.map(p => {
      const pend = finding.pending.find(x => x.kind === 'invite' && x.user_id === p.user_id)
      const btn = pend
        ? `<button type="button" class="btn sm" data-cancel-invite="${esc(pend.id)}">Cancel invite</button>`
        : `<button type="button" class="btn sm" data-invite="${esc(p.user_id)}"${(!canInvite || full) ? ' disabled' : ''}>${full ? 'Full' : 'Invite'}</button>`
      return row(esc, p.display_name, p.intro || '', btn)
    }).join('')
    return `<div class="sp-head"><h2 id="sp-h" tabindex="-1">Find people</h2><p>People in this event looking for a team. Invite someone and they choose whether to join.</p></div>${finding.solo.length ? `<div class="list">${rows}</div>` : `<div class="empty-state"><h3>Nobody is looking right now</h3><p>People who list themselves as looking for a team show here.</p></div>`}<p class="list-foot">Anyone on the team can invite. An invite ends when they join any team.</p>`
  }

  function settingsPanelHTML() {
    // Round-6 Cosmetic 3: "who can join" dropped -- that setting is gone
    // (Ask to join and the team link/code are just how joining works now,
    // not something admins turn on or off).
    const parts = [`<div class="sp-head"><h2 id="sp-h" tabindex="-1">Settings</h2><p>${isAdmin ? 'Admins change the team’s name, what it’s looking for, the lock and its link.' : 'Settings for ' + esc(team.name) + '.'}</p></div>`]
    if (isAdmin) {
      parts.push(`<section class="card"><h2>Team</h2><div class="field"><label for="set-name">Team name</label><input class="input" id="set-name" maxlength="80" value="${esc(team.name)}"${canAct ? '' : ' disabled'}></div><div class="field"><label for="set-looking">Looking for</label><input class="input" id="set-looking" maxlength="120" value="${esc(team.looking_for || '')}"${canAct ? '' : ' disabled'}><p class="help">Shown to people browsing teams. Leave it empty if you’re not looking.</p></div><div class="card-foot"><span class="hint" id="set-hint">No changes</span><button class="btn quiet" type="button" id="set-discard" disabled>Discard</button><button class="btn primary" type="button" id="set-save" disabled${canAct ? '' : ' disabled'}>Save</button></div></section>`)
      if (!frozen) {
        parts.push(`<section class="card"><h2>Joining</h2><div class="setting"><div class="t"><b>Lock team</b><span>No more people can join. The team link, requests and invites stop working, and the organiser’s Fill teams skips ${esc(team.name)}.</span></div><div class="ctl"><button class="toggle${team.locked ? ' on' : ''}" type="button" role="switch" aria-checked="${team.locked ? 'true' : 'false'}" aria-label="Lock team" id="lock-toggle"></button></div></div><div class="setting"><div class="t"><b>New team link</b><span>The old link and code stop working. People already on the team stay.</span></div><div class="ctl"><button class="btn" type="button" id="new-link">New link</button></div></div></section>`)
      }
    } else {
      const admins = (team.members || []).filter(m => m.admin).map(m => m.display_name || 'Someone')
      parts.push(`<section class="card"><h2>Team</h2><dl><dt>Team name</dt><dd>${esc(team.name)}</dd><dt>Looking for</dt><dd>${esc(team.looking_for || 'Not looking')}</dd></dl><p class="small muted">Only admins change these.${admins.length ? ' Admins: ' + esc(admins.join(', ')) + '.' : ''}</p></section>`)
    }
    if ((ev.tracks || []).length) {
      const current = (team.track && team.track.slug) || ''
      parts.push(`<section class="card"><h2>Track</h2><p class="sub">Which challenge ${esc(team.name)} is entering. Anyone on the team can change it.</p><div class="t-row"><select class="select" id="track-select" data-no-dirty aria-label="Track"${canAct ? '' : ' disabled'}><option value="">No track</option>${ev.tracks.map(t => `<option value="${esc(t.slug)}"${t.slug === current ? ' selected' : ''}>${esc(t.name)}</option>`).join('')}</select></div></section>`)
    }
    if (!frozen) {
      const members = team.members || []
      const onlyMember = members.length === 1
      if (onlyMember) {
        parts.push(`<section class="danger-zone"><h2>Leave team</h2><div class="setting"><div class="t"><span>You’re the only member, so ${esc(team.name)}, its entry and its website are deleted when you leave.</span></div><button class="btn danger" type="button" id="leave-team" data-only="1">Delete team and leave</button></div></section>`)
      } else {
        const successor = me.team.admin_successor
        const span = successor
          ? `You’ll lose access to ${esc(ui.possessive(team.name))} entry and website. ${esc(successor)} becomes admin when you leave.`
          : `You’ll lose access to ${esc(ui.possessive(team.name))} entry and website.`
        parts.push(`<section class="danger-zone"><h2>Leave team</h2><div class="setting"><div class="t"><span>${span}</span></div><button class="btn danger" type="button" id="leave-team">Leave team</button></div></section>`)
      }
    }
    return parts.join('')
  }

  function wireOnTeam(tab) {
    const members = team.members || []
    const unlockBtn = $('unlock-team')
    if (unlockBtn) unlockBtn.addEventListener('click', () => act(unlockBtn, () => api('PUT', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/team/lock', { locked: false })))
    const copyBtn = $('copy-team-link')
    if (copyBtn) copyBtn.addEventListener('click', async () => {
      try { await navigator.clipboard.writeText(copyBtn.getAttribute('data-link') || ''); copyBtn.textContent = 'Copied'; setTimeout(() => { copyBtn.innerHTML = COPY_ICON + 'Copy link' }, 2000) }
      catch (e) { ui.toast('Could not copy. Select the text and copy it.', copyBtn, true) }
    })
    main.querySelectorAll('[data-accept]').forEach(b => b.addEventListener('click', () => act(b, () => api('POST', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/requests/' + encodeURIComponent(b.getAttribute('data-accept')) + '/accept'))))
    main.querySelectorAll('[data-decline]').forEach(b => b.addEventListener('click', () => act(b, () => api('POST', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/requests/' + encodeURIComponent(b.getAttribute('data-decline')) + '/decline'))))
    main.querySelectorAll('[data-member] .row-menu button').forEach(item => {
      item.addEventListener('click', () => onMemberAct(item))
    })
    // Find people
    main.querySelectorAll('[data-invite]').forEach(b => b.addEventListener('click', () => act(b, () => api('POST', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/team/invites', { user_id: b.getAttribute('data-invite') }))))
    main.querySelectorAll('[data-cancel-invite]').forEach(b => b.addEventListener('click', () => act(b, () => api('DELETE', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/requests/' + encodeURIComponent(b.getAttribute('data-cancel-invite'))))))
    // Settings
    const setName = $('set-name'), setLooking = $('set-looking'), setDiscard = $('set-discard'), setSave = $('set-save'), setHint = $('set-hint')
    if (setName && setSave) {
      function checkDirty() {
        const dirty = setName.value !== team.name || setLooking.value !== (team.looking_for || '')
        setDiscard.disabled = !dirty
        setSave.disabled = !dirty
        setHint.textContent = dirty ? '' : 'No changes'
      }
      setName.addEventListener('input', checkDirty)
      setLooking.addEventListener('input', checkDirty)
      setDiscard.addEventListener('click', () => { setName.value = team.name; setLooking.value = team.looking_for || ''; checkDirty() })
      setSave.addEventListener('click', () => {
        const name = setName.value.trim()
        if (!name) { ui.toast('Enter a team name.', setSave, true); return }
        act(setSave, () => api('PATCH', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/team', { name, looking_for: setLooking.value }), { saved: true })
      })
    }
    const lockToggle = $('lock-toggle')
    if (lockToggle) lockToggle.addEventListener('click', onToggleLock)
    const newLink = $('new-link')
    if (newLink) newLink.addEventListener('click', onNewLink)
    const trackSelect = $('track-select')
    if (trackSelect) trackSelect.addEventListener('change', () => act(null, () => api('PUT', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/team/track', { track: trackSelect.value }), { saved: true }))
    const leaveBtn = $('leave-team')
    if (leaveBtn) leaveBtn.addEventListener('click', onLeaveTeam)
  }

  async function onMemberAct(item) {
    const row_ = item.closest('[data-member]')
    const userID = row_.getAttribute('data-member')
    const name = row_.getAttribute('data-name')
    const actName = item.getAttribute('data-act')
    if (actName === 'admin') {
      act(item, () => api('PUT', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/team/members/' + encodeURIComponent(userID) + '/admin', { admin: true }))
      return
    }
    // "Step down as admin" is the self-row's own version of "Remove as
    // admin" (acting on your own user id) -- both unset admin, same as
    // every other admin on the team can do to this one.
    if (actName === 'unadmin' || actName === 'stepdown') {
      act(item, () => api('PUT', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/team/members/' + encodeURIComponent(userID) + '/admin', { admin: false }))
      return
    }
    if (actName === 'remove') {
      // Round-6 Cosmetic 1: the question appears once, in the title -- the
      // body says only what happens, with the possessive routed through
      // ui.possessive (Round-6 Cosmetic 2) instead of a hand-built "'s"
      // that always added an extra s to a name already ending in one.
      const ok = await ui.confirm({ title: 'Remove ' + name + '?', body: name + ' loses access to ' + ui.possessive(team.name) + ' entry and website. The team link changes, so the old one stops working.', confirmLabel: 'Remove from team', danger: true })
      if (!ok) return
      act(item, () => api('DELETE', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/team/members/' + encodeURIComponent(userID)))
      return
    }
    if (actName === 'leave') { await onLeaveTeam(); return }
  }

  async function onToggleLock() {
    const wantLocked = !team.locked
    if (wantLocked) {
      const n = team.pending_requests || 0
      const ok = await ui.confirm({ title: 'Lock ' + team.name + '?', body: 'No more people can join.' + (n ? ' ' + n + ' pending request' + (n === 1 ? '' : 's') + ' and invites will be cleared.' : ''), confirmLabel: 'Lock team', danger: true })
      if (!ok) return
    }
    await act(null, () => api('PUT', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/team/lock', { locked: wantLocked }))
  }

  async function onNewLink() {
    const ok = await ui.confirm({ title: 'Make a new team link?', body: 'The old link and code stop working. People already on the team stay.', confirmLabel: 'New link', danger: true })
    if (!ok) return
    await act(null, () => api('POST', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/team/link'))
  }

  async function onLeaveTeam() {
    const onlyMember = (team.members || []).length === 1
    const successor = me.team.admin_successor
    const title_ = onlyMember ? 'Delete ' + team.name + ' and leave?' : 'Leave ' + team.name + '?'
    const tail = onlyMember
      ? 'You’re the only member, so ' + team.name + ', its entry and its website are deleted for good.'
      : 'You’ll lose access to its entry and website.' + (successor ? ' ' + successor + ' becomes admin.' : '') + ' You can join or start another team while teams are open.'
    const ok = await ui.confirm({ title: title_, body: tail, confirmLabel: onlyMember ? 'Delete team and leave' : 'Leave team', danger: true })
    if (!ok) return
    await act(null, () => api('POST', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/teams/leave'))
  }

  // -------------------------------------------------------------- paint --
  function paint() {
    let body
    if (!team) {
      body = noTeamHTML()
      main.innerHTML = `<div class="ph"><div class="t"><h1>Team</h1><p>You’re not on a team yet.</p></div></div>` + removalNote + body
      wireNoTeam()
      main.querySelectorAll('.card').forEach(root => { if (root.querySelector('input:not([name="way"])')) ui.trackForm(ctx, root) })
      return
    }
    const tab = resolveTab()
    if (params.get('tab') !== tab) {
      const fixed = `${location.pathname}?s=team&tab=${tab}`
      history.replaceState(history.state, '', fixed)
      ui.notePath(fixed)
    }
    const trackName = team.track ? team.track.name : 'No track'
    const lockedPill = team.locked ? ` <span class="pill warn" style="margin-left:6px">${LOCK_ICON}Locked</span>` : ''
    main.innerHTML = `<div class="ph"><div class="t"><h1>${esc(team.name)}${lockedPill}</h1><p>${esc(trackName)} · ${(team.members || []).length} of ${ev.team_size_max} people${isAdmin ? ' · You’re an admin' : ''}</p></div></div>
<div class="subnav-split">${subnavHTML(tab, (team.members || []).length)}<section class="sp" aria-labelledby="sp-h">${tab === 'members' ? membersPanelHTML() : tab === 'people' ? peoplePanelHTML() : settingsPanelHTML()}</section></div>`
    const h = main.querySelector('#sp-h')
    if (h) { h.setAttribute('tabindex', '-1'); h.focus() }
    wireOnTeam(tab)
    if (tab === 'settings') main.querySelectorAll('.sp .card').forEach(root => {
      if (root.querySelector('input:not([readonly]), textarea')) ui.trackForm(ctx, root)
    })
  }

  // No team, and nothing to do about it at all (ended, or every way is
  // closed): the one empty-state replaces everything (spec.json
  // "teams closed, no team").
  if (!team && !canJoinByCode && !canStart && !(finding.enabled && canDiscover)) {
    main.innerHTML = `<div class="ph"><div class="t"><h1>Team</h1><p>You’re not on a team.</p></div></div>${removalNote}<div class="empty-state">${LOCK_ICON}<h3>Teams can’t be started or joined now</h3><p>Entries are closed. If you should be on a team, ask the organiser.</p></div>`
  } else {
    paint()
  }

  // replay-participant-walkthrough: the shell's Help menu deep-links here
  // with &walkthrough=1 while Team is the current screen; tour.js itself
  // checks the stored seen-state (localStorage -- see tours.js
  // participantTeam). Auto-start only for someone who still has a team to
  // find: already on a team, or teams not open, has nothing here to point at.
  const openWalk = ctx.params.get('walkthrough') === '1'
  if (openWalk) startTour('participantTeam', ctx, { force: true })
  else if (!team && teamsOpen && !autoTourTried) { autoTourTried = true; startTour('participantTeam', ctx) }
}
