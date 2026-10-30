import { errText, isBlockedCode } from './_partB-util.js'
import { startTour } from '../tour.js'

export function title () { return 'Team' }

// Guided walkthrough: shown once, automatically, the first time a
// participant with no team opens Team while teams are open. Guarded so a
// refresh of this same screen doesn't keep popping it back open within one
// page load.
let autoTourTried = false

// "Just filled" (spec state) has no server signal: there is no field that
// says "an organiser's Fill teams placed you here", and no Go change is in
// scope for this batch. participant-home.js already approximates this by
// remembering the last-seen team per event and treating a none -> some
// transition as "just placed"; this screen does the same under its own key
// (it must not share home's key -- whichever screen renders first would
// "consume" the transition for the other). It also has an extra wrinkle
// home's version doesn't: this is the screen the join/start/accept buttons
// live on, so a self-service join must not show "placed by the organiser"
// about to the very team the user just joined themselves. selfJoined()
// marks that down right before the refresh those actions already do.
function justPlacedNote (slug, team) {
  const seenKey = 'sh-seen-team-page-' + slug
  const selfKey = 'sh-selfjoin-' + slug
  let seen = null
  let self = false
  try { seen = localStorage.getItem(seenKey) } catch (e) {}
  try { self = sessionStorage.getItem(selfKey) === '1'; sessionStorage.removeItem(selfKey) } catch (e) {}
  const teamSlug = team ? team.slug : ''
  try { localStorage.setItem(seenKey, teamSlug) } catch (e) {}
  return seen === '' && !!teamSlug && !self
}
function markSelfJoin (slug) { try { sessionStorage.setItem('sh-selfjoin-' + slug, '1') } catch (e) {} }

// Features on this screen: list-open-teams, set-looking-for-team,
// ask-to-join-team, withdraw-join-request, invite-solo-participant,
// cancel-sent-invite, accept-team-request-or-invite,
// decline-team-request-or-invite, set-team-open-and-looking-for,
// remove-teammate, choose-team-track, leave-team, start-a-team,
// join-team-by-code, copy-team-code.

export async function render (ctx) {
  const { main, api: client, ui, event: detail, refresh } = ctx
  const api = client.api
  const esc = ui.esc
  const ev = detail.event
  const me = detail.me
  const team = me.team

  main.innerHTML = '<div class="ph"><div class="t"><h1>Team</h1><p>' + esc(ev.title || '') + '</p></div></div><p class="note">Loading…</p>'

  const teamsOpen = (ev.stage === 'open' || ev.stage === 'building') && !ev.taken_down
  const ended = ev.stage === 'archived'
  const frozen = !!ev.taken_down || (team ? (typeof team.frozen === 'boolean' ? team.frozen : ev.stage === 'closed') : ev.stage === 'closed')
  const canStart = teamsOpen && !frozen && !(ev.submission_deadline && new Date(ev.submission_deadline) <= new Date())
  const canJoinByCode = teamsOpen && !ended
  const editable = teamsOpen && !frozen
  const justPlaced = justPlacedNote(ev.slug, team)

  let finding = { enabled: false, teams: [], solo: [], pending: [], looking: false, intro: '', team: null }
  try {
    const r = await api('GET', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/teams/open')
    if (r.ok) finding = Object.assign(finding, r.data)
  } catch (e) { if (client.isSignedOut(e)) return }

  const $ = id => main.querySelector('#' + id)

  async function act (btn, fn, { onBlocked } = {}) {
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
      await refresh()
    } catch (e) {
      if (client.isSignedOut(e)) return
      ui.toast('Cannot reach the server.', btn || main, true)
      if (btn) btn.disabled = false
    }
  }

  function row (main_, sub, actionsHTML) {
    return '<div class="r"><div class="main"><b>' + esc(main_) + '</b><span>' + esc(sub) + '</span></div>' + actionsHTML + '</div>'
  }

  const sections = []

  if (!team) {
    // ---- No-team state ----
    // S13: an invite waiting for you is the most actionable thing on this
    // screen, so it goes first -- it used to be the last section, easy to
    // miss below the open-teams list and the join/start cards.
    if (finding.enabled) {
      const invitesFirst = finding.pending.filter(p => p.kind === 'invite')
      if (invitesFirst.length) {
        sections.push('<h2 class="section-title">Invites for you</h2><div class="list">' + invitesFirst.map(p =>
          row(p.team_name, 'Invited you to join', '<button type="button" class="btn primary sm" data-accept="' + esc(p.id) + '">Accept</button><button type="button" class="btn sm" data-decline="' + esc(p.id) + '">Decline</button>')
        ).join('') + '</div>')
      }
    }
    if (finding.enabled) {
      const rows = finding.teams.map(t => {
        const req = finding.pending.find(p => p.kind === 'request' && p.team === t.slug)
        const full = t.member_count >= t.team_size_max
        const blocked = full || !canJoinByCode
        const members = Array.isArray(t.members) ? t.members.filter(Boolean) : []
        const sub = t.member_count + ' of ' + t.team_size_max +
          (t.track && t.track.name ? ' · ' + t.track.name : '') +
          (members.length ? ' · ' + members.join(', ') : '') +
          (t.looking_for && !full ? ' · Looking for: ' + t.looking_for : '')
        const btn = req
          ? '<button type="button" class="btn sm" data-withdraw="' + esc(req.id) + '">Withdraw request</button>'
          : '<button type="button" class="btn sm" data-ask="' + esc(t.slug) + '"' + (blocked ? ' disabled' : '') + '>' + (full ? 'Full' : 'Ask to join') + '</button>'
        return row(t.name, sub, btn)
      }).join('')
      sections.push('<div data-tour="open-teams"><h2 class="section-title">Teams looking for members</h2>' +
        (finding.teams.length ? '<div class="list">' + rows + '</div>' : '<div class="list"><div class="empty">No open teams right now.</div></div>') + '</div>')
    }
    // Team finding off only turns off discovery (the open-teams list, "be
    // found" and invites below) -- the API's createTeam has never required
    // it, so starting a private team (then sharing its code by hand) must
    // keep working either way; only joining-by-code is otherwise universal.
    const joinCard = '<div class="card" data-tour="join-team"><h2>Join a team</h2><div class="field"><label for="join-code">Team code</label><input class="input" id="join-code" maxlength="16" autocomplete="off" spellcheck="false" autocapitalize="off"></div><button type="button" class="btn" id="join-go"' + (canJoinByCode ? '' : ' disabled') + '>Join a team</button></div>'
    const startCard = '<div class="card" data-tour="start-team"><h2>Start a team</h2><div class="field"><label for="start-name">Team name</label><input class="input" id="start-name" maxlength="80" autocomplete="off"></div><button type="button" class="btn primary" id="start-go"' + (canStart ? '' : ' disabled') + '>Start a team</button></div>'
    sections.push('<div class="grid2">' + startCard + joinCard + '</div>')
    if (!canJoinByCode && !canStart) {
      sections.push('<p class="note">Teams can’t be started or joined now. Ask the organiser.</p>')
    }
    if (finding.enabled) {
      sections.push('<div class="card" data-tour="be-found"><h2>Be found</h2>' +
        '<label class="check"><input type="checkbox" id="look-on"' + (finding.looking ? ' checked' : '') + '><b>List me as looking for a team</b></label>' +
        '<div class="field"><label for="look-intro">Your intro</label><textarea class="input" id="look-intro" maxlength="120">' + esc(finding.intro || '') + '</textarea></div>' +
        '<button type="button" class="btn" id="look-save">Save intro</button></div>')
      const orphanRequests = finding.pending.filter(p => p.kind === 'request' && !finding.teams.some(t => t.slug === p.team))
      if (orphanRequests.length) {
        sections.push('<h2 class="section-title">Your pending requests</h2><div class="list">' + orphanRequests.map(p =>
          row(p.team_name, 'Waiting for a reply', '<button type="button" class="btn sm" data-withdraw="' + esc(p.id) + '">Withdraw request</button>')
        ).join('') + '</div>')
      }
    }
  } else {
    // ---- With-team state ----
    // Non-attributing (C9): the API can't tell an organiser's Fill teams
    // placement apart from a self-service join or a teammate accepting a
    // request, so the note never says "the organiser placed you".
    if (justPlaced) sections.push('<div class="note ok">You are on ' + esc(team.name) + ' now.</div>')
    if (!editable) sections.push('<p class="note">Teams are locked once entries close.</p>')
    const members = team.members || []
    const full = members.length >= ev.team_size_max
    const memberRows = members.map(m => {
      const canRemove = editable && !m.you && m.user_id && members.length > 1
      const btn = canRemove ? '<button type="button" class="btn danger sm" data-remove="' + esc(m.user_id) + '" data-name="' + esc(m.display_name || 'this person') + '">Remove</button>' : ''
      return row(m.display_name || 'Someone', m.you ? 'You' : 'Teammate', btn)
    }).join('')
    sections.push('<div class="card"><h2>' + esc(team.name || '') + '</h2><p class="sub">' + members.length + ' of ' + esc(String(ev.team_size_max)) + ' people</p>' +
      '<div class="copy"><input class="input" id="team-code" aria-label="Team code" value="' + esc(team.code || '') + '" readonly></div>')

    if (finding.enabled && editable) {
      const open = finding.team ? !!finding.team.open : true
      sections.push('<hr class="rule"><div class="field"><span class="lab">Who can join</span><div class="seg"><span tabindex="0" role="button" class="' + (open ? 'on' : '') + '" data-seg="true">Anyone</span><span tabindex="0" role="button" class="' + (!open ? 'on' : '') + '" data-seg="false">Only people with the code</span></div></div>' +
        '<div class="field"><label for="team-looking">Looking for</label><input class="input" id="team-looking" maxlength="120" value="' + esc((finding.team && finding.team.looking_for) || '') + '"><p class="help">Shown to people browsing teams, for example "a designer".</p></div>' +
        '<button type="button" class="btn" id="team-settings-save">Save</button>')
    }
    sections.push('</div>')

    sections.push('<h2 class="section-title">Members</h2><div class="list">' + memberRows + '</div>')

    if (finding.enabled) {
      if (finding.solo.length) {
        sections.push('<h2 class="section-title">Looking for a teammate?</h2><div class="list">' + finding.solo.map(p => {
          const pend = finding.pending.find(x => x.kind === 'invite' && x.user_id === p.user_id)
          const btn = pend
            ? '<button type="button" class="btn sm" data-cancel-invite="' + esc(pend.id) + '">Cancel invite</button>'
            : '<button type="button" class="btn sm" data-invite="' + esc(p.user_id) + '"' + (!editable || full ? ' disabled' : '') + '>' + (full ? 'Full' : 'Invite') + '</button>'
          return row(p.display_name, p.intro || '', btn)
        }).join('') + '</div>')
      }
      const requests = finding.pending.filter(p => p.kind === 'request')
      if (requests.length) {
        sections.push('<h2 class="section-title">Requests to join</h2><div class="list">' + requests.map(p =>
          row(p.display_name, 'Wants to join', '<button type="button" class="btn primary sm" data-accept="' + esc(p.id) + '"' + (!editable || full ? ' disabled' : '') + '>Accept</button><button type="button" class="btn sm" data-decline="' + esc(p.id) + '"' + (!editable ? ' disabled' : '') + '>Decline</button>')
        ).join('') + '</div>')
      }
    }

    if ((ev.tracks || []).length) {
      const current = (team.track && team.track.slug) || ''
      sections.push('<div class="card"><div class="field"><label for="team-track">Track</label><select class="select" id="team-track"' + (editable ? '' : ' disabled') + '><option value="">No track</option>' +
        ev.tracks.map(t => '<option value="' + esc(t.slug) + '"' + (t.slug === current ? ' selected' : '') + '>' + esc(t.name) + '</option>').join('') + '</select></div>' +
        (editable ? '<button type="button" class="btn" id="team-track-save">Save track</button>' : '') + '</div>')
    }

    if (editable) sections.push('<div class="danger-zone"><h2>Leave team</h2><div class="setting"><div class="t"><span>You’ll lose access to this team’s entry and site.</span></div><button type="button" class="btn danger" id="team-leave">Leave team</button></div></div>')
  }

  main.innerHTML = '<div class="ph"><div class="t"><h1>Team</h1><p>' + esc(ev.title || '') + '</p></div></div>' + sections.join('')

  // ---- wire up ----
  const copyInput = $('team-code')
  if (copyInput) copyInput.parentElement.append(ui.copyButton(team.code || '', 'Copy join code'))

  main.querySelectorAll('[data-ask]').forEach(b => b.addEventListener('click', () => act(b, () => api('POST', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/teams/' + encodeURIComponent(b.getAttribute('data-ask')) + '/requests'))))
  main.querySelectorAll('[data-withdraw]').forEach(b => b.addEventListener('click', () => act(b, () => api('DELETE', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/requests/' + encodeURIComponent(b.getAttribute('data-withdraw'))))))
  main.querySelectorAll('[data-accept]').forEach(b => b.addEventListener('click', () => act(b, () => api('POST', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/requests/' + encodeURIComponent(b.getAttribute('data-accept')) + '/accept'))))
  main.querySelectorAll('[data-decline]').forEach(b => b.addEventListener('click', () => act(b, () => api('POST', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/requests/' + encodeURIComponent(b.getAttribute('data-decline')) + '/decline'))))
  main.querySelectorAll('[data-invite]').forEach(b => b.addEventListener('click', () => act(b, () => api('POST', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/team/invites', { user_id: b.getAttribute('data-invite') }))))
  main.querySelectorAll('[data-cancel-invite]').forEach(b => b.addEventListener('click', () => act(b, () => api('DELETE', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/requests/' + encodeURIComponent(b.getAttribute('data-cancel-invite'))))))
  main.querySelectorAll('[data-remove]').forEach(b => b.addEventListener('click', async () => {
    const name = b.getAttribute('data-name')
    const ok = await ui.confirm({ title: 'Take ' + name + ' off the team?', body: name + ' loses access to this team’s entry and site. The team gets a new join code.', confirmLabel: 'Remove', danger: true })
    if (!ok) return
    act(b, () => api('DELETE', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/team/members/' + encodeURIComponent(b.getAttribute('data-remove'))))
  }))

  const startGo = $('start-go')
  if (startGo) startGo.addEventListener('click', () => {
    const name = $('start-name').value.trim()
    if (!name) { ui.toast('Enter a team name.', startGo, true); return }
    act(startGo, () => api('POST', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/teams', { name }))
  })
  const joinGo = $('join-go')
  if (joinGo) joinGo.addEventListener('click', () => {
    const code = $('join-code').value.trim()
    if (!code) { ui.toast('Enter a team code.', joinGo, true); return }
    act(joinGo, () => api('POST', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/teams/join', { code }))
  })
  const lookOn = $('look-on')
  const lookSave = $('look-save')
  if (lookOn) lookOn.addEventListener('change', () => act(null, () => api('PUT', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/me/looking', { looking: lookOn.checked, intro: $('look-intro').value })))
  if (lookSave) lookSave.addEventListener('click', () => act(lookSave, () => api('PUT', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/me/looking', { looking: lookOn.checked, intro: $('look-intro').value })))

  let pendingOpen = finding.team ? !!finding.team.open : true
  if (editable) {
    main.querySelectorAll('[data-seg]').forEach(s => s.addEventListener('click', () => {
      pendingOpen = s.getAttribute('data-seg') === 'true'
      main.querySelectorAll('[data-seg]').forEach(x => x.classList.toggle('on', x === s))
    }))
  }
  const settingsSave = $('team-settings-save')
  if (settingsSave) settingsSave.addEventListener('click', () => act(settingsSave, () => api('PUT', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/team/finding', { open: pendingOpen, looking_for: $('team-looking').value })))

  const trackSave = $('team-track-save')
  if (trackSave) trackSave.addEventListener('click', () => act(trackSave, () => api('PUT', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/team/track', { track: $('team-track').value })))

  const leave = $('team-leave')
  if (leave) leave.addEventListener('click', async () => {
    const ok = await ui.confirm({ title: 'Leave ' + (team.name || 'this team') + '?', body: 'You’ll lose access to this team’s entry and site. You can join or start another while the event is open.', confirmLabel: 'Leave', danger: true })
    if (!ok) return
    act(leave, () => api('POST', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/teams/leave'))
  })

  // replay-participant-walkthrough: the shell's Help menu deep-links here
  // with &walkthrough=1 while Team is the current screen; tour.js itself
  // checks the stored seen-state (localStorage -- see tours.js
  // participantTeam). Auto-start only for someone who still has a team to
  // find: already on a team, or teams not open, has nothing here to point at.
  const openWalk = ctx.params.get('walkthrough') === '1'
  if (openWalk) startTour('participantTeam', ctx, { force: true })
  else if (!team && teamsOpen && !autoTourTried) { autoTourTried = true; startTour('participantTeam', ctx) }
}
