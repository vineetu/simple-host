import { errText, adminLock } from './_orgA-util.js'

export function title(ctx) { return 'People' }

function personCategory(p) {
  if (p.role === 'organiser') return p.primary_organiser ? 'Organiser' : 'Co-organiser'
  if (p.role === 'judge') return 'Judge'
  return 'Participant'
}

export async function render(ctx) {
  const { main, api: client, ui, event: data } = ctx
  const api = client.api
  const ev = data.event
  const admin = !!data.admin_view
  const path = '/v1/hack/events/' + encodeURIComponent(ev.slug)
  const setLive = (msg, kind, near) => { if (msg) ui.toast(msg, near || main, kind === 'err') }

  let applications = [], people = [], questions = [], approvalRequired = true
  // savedQuestions is an independent snapshot of the last-known-saved questions
  // (own clones, not the same array/objects as the draft `questions`), so
  // Add/Remove edits to the draft can never leak into the body the approval
  // toggle sends -- see the signup-approval handler below.
  let savedQuestions = []
  let registrationError = false // true when /registration failed to load: Save questions and the approval toggle are refused below so an empty default never replaces saved sign-up questions
  try {
    const [a, p, r] = await Promise.all([api('GET', path + '/applications'), api('GET', path + '/people'), api('GET', path + '/registration')])
    if (a.ok) applications = a.data || []
    if (p.ok) people = p.data || []
    if (r.ok) { questions = r.data.questions || []; approvalRequired = !!r.data.approval_required; savedQuestions = questions.map(q => ({ ...q })) }
    else registrationError = true
  } catch (e) { registrationError = true }

  function guardRegistration(btn) {
    if (!registrationError) return false
    setLive('Could not load sign-up questions earlier. Reload the page before saving here.', 'err', btn)
    return true
  }

  let roleFilter = 'Everyone'
  let invite = null // { url, expires_at } for this session only; the API has no GET for a pending invite.

  function applicationCard(a) {
    return `<div class="r"><div class="main"><b>${ui.esc(a.display_name)}</b><span>Applied ${ui.esc(ui.dateTime(a.joined_at, ev))}</span></div></div>
<div class="card" data-app="${ui.esc(a.user_id)}">${(a.answers || []).map(q => `<p><b>${ui.esc(q.prompt)}</b><br>${ui.esc(q.answer || '—')}</p>`).join('')}
<div class="row-flex"><button class="btn primary" type="button" data-app-approve>Approve</button><button class="btn danger sm" type="button" data-app-reject title="${ui.esc(a.display_name)} stays a member but can't start or join a team or publish for this event. This can't be undone by re-approving later. Reject this application?">Reject</button></div></div>`
  }
  function personRow(p) {
    const bits = [personCategory(p), { pending: 'Pending', approved: 'Approved', rejected: 'Not approved' }[p.approval_status] || 'Approved']
    if (p.team && (p.team.name || p.team.slug)) bits.push(p.team.name || p.team.slug)
    const joined = ui.dateTime(p.joined_at, ev)
    if (joined) bits.push(joined)
    let actions = ''
    if (p.primary_organiser) actions = '<span class="hint muted">Event creator</span>'
    else if (p.role === 'organiser') actions = `<details><summary class="small muted">Actions</summary><button class="btn danger sm" type="button" data-remove-organiser="${ui.esc(p.user_id)}" data-name="${ui.esc(p.display_name)}" title="They lose manage access to this event right away. (The event's original creator can't be removed.)">Remove co-organiser</button></details>`
    else {
      const keyBtn = p.has_key ? `<button class="btn danger sm" type="button" data-turn-off-key="${ui.esc(p.user_id)}" data-name="${ui.esc(p.display_name)}" title="Their key stops working at once. They'd need to create a new one themselves.">Turn off publishing key</button>` : ''
      actions = `<details><summary class="small muted">Actions</summary><button class="btn danger sm" type="button" data-remove-person="${ui.esc(p.user_id)}" data-name="${ui.esc(p.display_name)}" data-role="${ui.esc(p.role)}">Remove from event</button>${keyBtn}</details>`
    }
    return `<div class="r"><div class="main"><b>${ui.esc(p.display_name || 'Someone')}</b><span>${ui.esc(bits.filter(Boolean).join(' · '))}</span></div>${actions}</div>`
  }
  function organiserLabel(p) {
    const you = !!(ctx.me && p.user_id && ctx.me.id === p.user_id)
    if (p.primary_organiser) return p.display_name + (you ? ' (you, creator)' : ' (creator)')
    return p.display_name + (you ? ' (you)' : '')
  }
  function questionRow(q, i) {
    return `<div class="row-flex" data-q-row="${i}"><input class="input" data-q-prompt="${i}" type="text" maxlength="160" value="${ui.esc(q.prompt || '')}" placeholder="Question"><label class="check"><input type="checkbox" data-q-required="${i}"${q.required ? ' checked' : ''}><span>Required</span></label><button class="btn danger sm" type="button" data-q-rm="${i}">Remove</button></div>`
  }

  function paint() {
    const $ = id => main.querySelector('#' + id)
    const pending = applications.filter(a => a.status === 'pending')
    const visiblePeople = people.filter(p => roleFilter === 'Everyone' || (roleFilter === 'Organisers' && p.role === 'organiser') || (roleFilter === 'Participants' && p.role === 'participant') || (roleFilter === 'Judges' && p.role === 'judge') || (roleFilter === 'Co-organisers' && p.role === 'organiser' && !p.primary_organiser))

    main.innerHTML = `
<div class="ph"><div class="t"><h1>People</h1><p>${pending.length ? pending.length + ' application' + (pending.length === 1 ? '' : 's') + ' waiting on you' : 'Nothing waiting on you'}</p></div></div>
<h2 class="section-title">Applications</h2>
${pending.length ? `<div id="app-list">${pending.map(applicationCard).join('')}</div>` : `<div class="note">${ev.stage === 'draft' ? "Sign-up isn't open yet. Move the event to Open to start taking applications." : 'Nothing waiting on you'}</div>`}

<details class="card" open><summary>People</summary>
<div class="field"><label>Filter by role<select class="select" id="role-filter" data-admin-live><option${roleFilter === 'Everyone' ? ' selected' : ''}>Everyone</option><option${roleFilter === 'Organisers' ? ' selected' : ''}>Organisers</option><option${roleFilter === 'Participants' ? ' selected' : ''}>Participants</option><option${roleFilter === 'Judges' ? ' selected' : ''}>Judges</option><option${roleFilter === 'Co-organisers' ? ' selected' : ''}>Co-organisers</option></select></label></div>
<div class="list" id="people-list">${visiblePeople.length ? visiblePeople.map(personRow).join('') : '<div class="empty">Nobody matches that filter.</div>'}</div>
</details>

<details class="card" id="signup"><summary>Co-organisers</summary>
<p>Current: ${ui.esc((people.filter(p => p.role === 'organiser').map(organiserLabel)).join(', '))}</p>
<p>A co-organiser can do everything you can on this event, including Delete this event. Only the creator can’t be removed.</p>
<button class="btn" type="button" id="coorg-create">Create invitation</button>
${invite ? `<div class="field"><label>Invitation<input class="input mono" id="coorg-url" type="text" value="${ui.esc(invite.url)}" readonly></label></div><button class="btn" type="button" id="coorg-copy">Copy invitation</button><button class="btn danger sm" type="button" id="coorg-revoke">Revoke invitation</button>` : ''}
</details>

<details class="card"><summary>Sign-up</summary>
${registrationError ? '<div class="note bad">Sign-up questions could not be loaded. Saving here is turned off until you reload the page.</div>' : ''}
<label class="check"><input type="checkbox" id="signup-approval"${approvalRequired ? ' checked' : ''}${registrationError ? ' disabled' : ''}><b>Approve participants before they can join or publish with a team</b></label>
<div id="q-rows">${questions.map(questionRow).join('')}</div>
<div class="row-flex"><button class="btn" type="button" id="q-add"${questions.length >= 8 || registrationError ? ' disabled' : ''}>Add question</button><button class="btn primary" type="button" id="q-save"${registrationError ? ' disabled' : ''}>Save questions</button></div>
</details>`

    adminLock(main, admin)

    $('role-filter').addEventListener('change', () => { roleFilter = $('role-filter').value; paint() })

    main.querySelectorAll('[data-app-approve]').forEach(btn => btn.addEventListener('click', () => decide(btn, 'approved')))
    main.querySelectorAll('[data-app-reject]').forEach(btn => btn.addEventListener('click', () => decide(btn, 'rejected')))
    async function decide(btn, decision) {
      const card = btn.closest('[data-app]')
      const id = card.getAttribute('data-app')
      if (decision === 'rejected') {
        const ok = await ui.confirm({ title: 'Reject this application?', body: btn.getAttribute('title'), confirmLabel: 'Reject', danger: true })
        if (!ok) return
      }
      btn.disabled = true
      try {
        const r = await api('POST', path + '/applications/' + encodeURIComponent(id) + '/decision', { decision })
        if (!r.ok) { setLive(errText(r), 'err', btn); btn.disabled = false; return }
        const [a2, p2] = await Promise.all([api('GET', path + '/applications'), api('GET', path + '/people')])
        if (a2.ok) applications = a2.data || []
        if (p2.ok) people = p2.data || []
        setLive('Application ' + decision + '.', 'ok')
        paint()
      } catch (e) { setLive('Cannot reach the server.', 'err', btn); btn.disabled = false }
    }

    main.querySelectorAll('[data-remove-person]').forEach(btn => btn.addEventListener('click', async () => {
      const id = btn.getAttribute('data-remove-person'); const name = btn.getAttribute('data-name') || 'this person'; const role = btn.getAttribute('data-role')
      const body = role === 'judge'
        ? name + ' is removed from the event. They can rejoin with the current join link unless you regenerate it.'
        : name + ' and their team membership are removed. They can rejoin with the current join link unless you regenerate it.'
      const ok = await ui.confirm({ title: 'Remove ' + name + ' from this event?', body, confirmLabel: 'Remove', danger: true })
      if (!ok) return
      btn.disabled = true
      try {
        const r = await api('DELETE', path + '/people/' + encodeURIComponent(id))
        if (!r.ok) { setLive(errText(r), 'err', btn); btn.disabled = false; return }
        const p2 = await api('GET', path + '/people')
        if (p2.ok) people = p2.data || []
        setLive('Removed', 'ok')
        paint()
      } catch (e) { setLive('Cannot reach the server.', 'err', btn); btn.disabled = false }
    }))
    main.querySelectorAll('[data-turn-off-key]').forEach(btn => btn.addEventListener('click', async () => {
      const id = btn.getAttribute('data-turn-off-key'); const name = btn.getAttribute('data-name') || 'this person'
      const ok = await ui.confirm({ title: "Turn off " + name + "'s key?", body: "Their key stops working at once. They'd need to create a new one themselves.", confirmLabel: 'Turn off key', danger: true })
      if (!ok) return
      btn.disabled = true
      try {
        const r = await api('DELETE', path + '/people/' + encodeURIComponent(id) + '/key')
        if (!r.ok) { setLive(errText(r), 'err', btn); btn.disabled = false; return }
        const p2 = await api('GET', path + '/people')
        if (p2.ok) people = p2.data || []
        setLive('Key turned off', 'ok')
        paint()
      } catch (e) { setLive('Cannot reach the server.', 'err', btn); btn.disabled = false }
    }))
    main.querySelectorAll('[data-remove-organiser]').forEach(btn => btn.addEventListener('click', async () => {
      const id = btn.getAttribute('data-remove-organiser'); const name = btn.getAttribute('data-name') || 'this co-organiser'
      const ok = await ui.confirm({ title: 'Remove ' + name + ' as co-organiser?', body: "They lose manage access to this event right away. (The event's original creator can't be removed.)", confirmLabel: 'Remove co-organiser', danger: true })
      if (!ok) return
      btn.disabled = true
      try {
        const r = await api('DELETE', path + '/organisers/' + encodeURIComponent(id))
        if (!r.ok) { setLive(errText(r), 'err', btn); btn.disabled = false; return }
        const p2 = await api('GET', path + '/people')
        if (p2.ok) people = p2.data || []
        setLive('Removed', 'ok')
        paint()
      } catch (e) { setLive('Cannot reach the server.', 'err', btn); btn.disabled = false }
    }))

    $('coorg-create').addEventListener('click', async () => {
      const ok = await ui.confirm({ title: 'Create co-organiser invitation?', body: 'This link grants full control of this event, including deleting it. Create it?', confirmLabel: 'Create invitation' })
      if (!ok) return
      const btn = $('coorg-create'); btn.disabled = true
      try {
        const r = await api('POST', path + '/organiser-invite')
        if (!r.ok) { setLive(errText(r), 'err', btn); btn.disabled = false; return }
        invite = { url: r.data.url, expires_at: r.data.expires_at }
        setLive('Invitation created. Share it with one co-organiser.', 'ok')
        paint()
      } catch (e) { setLive('Cannot reach the server.', 'err', btn); btn.disabled = false }
    })
    const copyBtn = $('coorg-copy')
    if (copyBtn) copyBtn.addEventListener('click', async () => {
      try { await navigator.clipboard.writeText($('coorg-url').value); copyBtn.textContent = 'Copied'; setTimeout(() => copyBtn.textContent = 'Copy invitation', 2000) }
      catch (e) { setLive('Could not copy. Select the text and copy it.', 'err') }
    })
    const revokeBtn = $('coorg-revoke')
    if (revokeBtn) revokeBtn.addEventListener('click', async () => {
      const ok = await ui.confirm({ title: 'Revoke this invitation?', body: 'This invitation stops working. Revoke it?', confirmLabel: 'Revoke', danger: true })
      if (!ok) return
      revokeBtn.disabled = true
      try {
        const r = await api('DELETE', path + '/organiser-invite')
        if (!r.ok) { setLive(errText(r), 'err', revokeBtn); revokeBtn.disabled = false; return }
        invite = null
        setLive('Invitation revoked.', 'ok')
        paint()
      } catch (e) { setLive('Cannot reach the server.', 'err', revokeBtn); revokeBtn.disabled = false }
    })

    // Collecting draft rows before any mutate+paint keeps an edit that is
    // still focused from being wiped out by the repaint that Add/Remove
    // triggers (the paint() for the new/shortened list is otherwise built
    // straight from the stale in-memory `questions`).
    function collectQuestions() {
      questions.forEach((q, i) => {
        const el = main.querySelector(`[data-q-prompt="${i}"]`)
        if (!el) return
        q.prompt = el.value
        q.required = main.querySelector(`[data-q-required="${i}"]`).checked
      })
    }
    // Generates an id that is not already used anywhere in `questions`,
    // independent of array position -- assigning by index alone (the old
    // 'q' + (i + 1)) can mint an id that an earlier, still-present question
    // already owns once a question has been removed and the array has shifted.
    function assignQuestionIds() {
      const used = new Set(questions.map(q => q.id).filter(Boolean))
      let n = 1
      questions.forEach(q => {
        if (q.id) return
        while (used.has('q' + n)) n++
        q.id = 'q' + n
        used.add(q.id)
      })
    }
    $('q-add').addEventListener('click', () => { if (questions.length < 8) { collectQuestions(); questions.push({ id: '', prompt: '', required: false }); paint() } })
    main.querySelectorAll('[data-q-rm]').forEach(btn => btn.addEventListener('click', () => { collectQuestions(); questions.splice(Number(btn.dataset.qRm), 1); paint() }))
    $('q-save').addEventListener('click', async () => {
      if (guardRegistration($('q-save'))) return
      const btn = $('q-save'); btn.disabled = true
      collectQuestions()
      assignQuestionIds()
      try {
        const r = await api('PUT', path + '/registration', { questions, approval_required: $('signup-approval').checked })
        if (!r.ok) { setLive(errText(r), 'err', btn); btn.disabled = false; return }
        questions = r.data.questions || questions
        // savedQuestions is cloned here, not aliased to `questions` -- the
        // draft array is about to keep being mutated by Add/Remove, and the
        // approval toggle below must only ever send what was last saved.
        savedQuestions = questions.map(q => ({ ...q }))
        approvalRequired = !!r.data.approval_required
        setLive('Questions saved.', 'ok', btn)
        paint()
      } catch (e) { setLive('Cannot reach the server.', 'err', btn); btn.disabled = false }
    })
    $('signup-approval').addEventListener('change', async () => {
      if (guardRegistration($('signup-approval'))) { $('signup-approval').checked = approvalRequired; return }
      const box = $('signup-approval'); const prev = !box.checked; box.disabled = true
      let admitPending = false
      const pendingNow = applications.filter(a => a.status === 'pending')
      if (!box.checked && pendingNow.length) {
        admitPending = await ui.confirm({ title: 'Waiting applications', body: pendingNow.length + ' people are still waiting. Approve all of them now, or leave them pending — you can still approve or reject each one later from this list. New joiners after this point won’t need approval either way.', confirmLabel: 'Approve all now', cancelLabel: 'Leave them pending' })
      }
      try {
        const r = await api('PUT', path + '/registration', { questions: savedQuestions, approval_required: box.checked, admit_pending: admitPending })
        if (!r.ok) { box.checked = prev; setLive(errText(r), 'err', box); box.disabled = false; return }
        approvalRequired = box.checked
        box.disabled = false
        if (admitPending) {
          const [a2, p2] = await Promise.all([api('GET', path + '/applications'), api('GET', path + '/people')])
          if (a2.ok) applications = a2.data || []
          if (p2.ok) people = p2.data || []
        }
        setLive('Saved', 'ok', box.closest('label'))
        paint()
      } catch (e) { box.checked = prev; setLive('Cannot reach the server.', 'err', box); box.disabled = false }
    })
  }

  paint()
}
