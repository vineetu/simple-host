import { href } from '../nav.js'
import { wantsSection } from '../platform.js'
import { adminLock } from './_orgA-util.js'

export async function render(ctx, section) {
  const { main, api: client, ui, event: data } = ctx
  const api = client.api
  const ev = data.event
  const admin = !!data.admin_view
  const path = '/v1/hack/events/' + encodeURIComponent(ev.slug)
  const setLive = ui.live(main)

  let applications = [], people = [], questions = [], approvalRequired = true
  // applicationsOpen mirrors the Settings switch, read-only here (D10: the
  // switch itself lives in Settings; People -> Sign-up only shows its state
  // with a link there). Defaults true (applications are on by default for
  // every event) so a load failure never claims they're off.
  let applicationsOpen = true
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
    if (r.ok) { questions = r.data.questions || []; approvalRequired = !!r.data.approval_required; applicationsOpen = r.data.applications_open !== false; savedQuestions = questions.map(q => ({ ...q })) }
    else registrationError = true
  } catch (e) { registrationError = true }
  // A render left running past a newer navigation (Judging opened while
  // People's own fetch was still in flight) must not go on to replace
  // `main` with its own, now-stale screen the instant this fetch resolves
  // -- organiser-overview.js already checks ctx.alive() after its own
  // async /people+/applications fetch, before painting; this screen had no
  // equivalent (Codex review finding 4, SIM-344 phase 2).
  if (!ctx.alive()) return

  function guardRegistration(btn) {
    if (!registrationError) return false
    setLive('Could not load sign-up questions earlier. Reload the page before saving here.', 'err', btn)
    return true
  }

  let roleFilter = 'Everyone'
  let invite = null // { url, expires_at } for this session only; the API has no GET for a pending invite.
  // C8: the Co-organisers and Sign-up sections (and the hash-targeted one on
  // first load) must stay open across a repaint, not just across the user's
  // own click -- every Add/Create/Remove below ends in paint(), which
  // rebuilds these <details> from scratch with no `open` by default. These
  // two flags are the source of truth paint() renders from; the `toggle`
  // listener below keeps them in sync with whatever the person just did by
  // hand, independent of why either was opened.
  let coorgOpen = false
  let signupOpen = wantsSection('signup')
  // Applications filter row (spec.json organiser-people "Applications"
  // section): client-side only, shown once anything is waiting. 'from' is
  // 'all'|'applied'|'link', 'age' is '' (Any) or an age_range value,
  // 'country' is '' (Any) or an ISO code, 'sort' is 'oldest'|'newest' --
  // the API already returns pending applications oldest-first, so "newest"
  // is just that list reversed, not a real re-sort.
  let appFrom = 'all', appAge = '', appCountry = '', appSort = 'oldest'

  function organiserLabel(p) {
    const you = !!(ctx.me && p.user_id && ctx.me.id === p.user_id)
    if (p.primary_organiser) return p.display_name + (you ? ' (you, creator)' : ' (creator)')
    return p.display_name + (you ? ' (you)' : '')
  }
  function questionRow(q, i) {
    return `<div class="control-row" data-q-row="${i}"><input class="input" data-q-prompt="${i}" type="text" maxlength="160" value="${ui.esc(q.prompt || '')}" placeholder="Question" aria-label="Question ${i + 1}"><label class="check"><input type="checkbox" data-q-required="${i}"${q.required ? ' checked' : ''}><span>Required</span></label><button class="btn danger sm" type="button" data-q-rm="${i}">Remove</button></div>`
  }

  function paint() { return ui.forms.preserve(ctx, () => paintContent()) }
  function paintContent() {
    if (!ctx.alive()) return
    const $ = id => main.querySelector('#' + id), esc = ui.esc
    if (section === 'organisers') {
      main.innerHTML = `<div class="list">${people.filter(p => p.role === 'organiser').map(p => `<div class="r"><span class="main"><b>${esc(organiserLabel(p))}</b></span>${p.primary_organiser ? '<span class="meta">Event creator</span>' : `<button class="btn danger sm" data-remove-organiser="${esc(p.user_id)}" data-name="${esc(p.display_name)}">Remove co-organiser</button>`}</div>`).join('')}</div><section class="card" id="coorg"><h2>Organisers</h2>
<p>Current: ${ui.esc((people.filter(p => p.role === 'organiser').map(organiserLabel)).join(', '))}</p>
<p>A co-organiser can do everything you can on this event, including Delete this event. Only the creator can’t be removed.</p>
<div class="form-actions"><button class="btn" type="button" id="coorg-create">Create invitation</button></div>
${invite ? `<div class="field"><label for="coorg-url">Invitation</label><div class="control-row"><input class="input mono" id="coorg-url" type="text" value="${ui.esc(invite.url)}" readonly><button class="btn" type="button" id="coorg-copy">Copy invitation</button></div></div><div class="form-actions"><button class="btn danger" type="button" id="coorg-revoke">Revoke invitation</button></div>` : ''}
</section>`
    main.querySelectorAll('[data-remove-organiser]').forEach(btn => btn.addEventListener('click', async () => {
      const id = btn.getAttribute('data-remove-organiser'); const name = btn.getAttribute('data-name') || 'this co-organiser'
      const ok = await ui.confirm({ title: 'Remove ' + name + ' as co-organiser?', body: "They lose manage access to this event right away. (The event's original creator can't be removed.)", confirmLabel: 'Remove co-organiser', danger: true })
      if (!ok) return
      btn.disabled = true
      try {
        const r = await api('DELETE', path + '/organisers/' + encodeURIComponent(id))
        if (!r.ok) { setLive(ui.errText(r), 'err', btn); btn.disabled = false; return }
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
        if (!r.ok) { setLive(ui.errText(r), 'err', btn); btn.disabled = false; return }
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
        if (!r.ok) { setLive(ui.errText(r), 'err', revokeBtn); revokeBtn.disabled = false; return }
        invite = null
        setLive('Invitation revoked.', 'ok')
        paint()
      } catch (e) { setLive('Cannot reach the server.', 'err', revokeBtn); revokeBtn.disabled = false }
    })

    } else {
      main.innerHTML = `<section class="card" id="signup"><h2>Sign-up</h2>
${registrationError ? '<div class="note bad">Sign-up questions could not be loaded. Saving here is turned off until you reload the page.</div>' : ''}
<label class="check"><input type="checkbox" id="signup-approval"${approvalRequired ? ' checked' : ''}${registrationError ? ' disabled' : ''}><span><b>Approve people who join with your link before they can join a team or publish</b><span>Applications from Find a hackathon and your event page always wait for you, whatever this says. Applications are ${applicationsOpen ? 'on' : 'off'} — <a href="${esc(href('organiser', ev.slug, 'settings'))}#applications">change in Settings</a>.</span></span></label>
<p class="help">Applicants also get three optional fields of ours: where they're based, age range and gender.</p>
<div id="q-rows">${questions.map(questionRow).join('')}</div>
<div class="form-actions"><button class="btn" type="button" id="q-add"${questions.length >= 8 || registrationError ? ' disabled' : ''}>Add question</button><button class="btn" type="button" id="q-save"${registrationError ? ' disabled' : ''}>Save questions</button></div>
</section>`
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
    main.querySelectorAll('[data-q-prompt], [data-q-required]').forEach(n => n.addEventListener('input', collectQuestions))
    $('q-add').addEventListener('click', () => { if (questions.length < 8) { collectQuestions(); questions.push({ id: '', prompt: '', required: false }); paint() } })
    main.querySelectorAll('[data-q-rm]').forEach(btn => btn.addEventListener('click', () => { collectQuestions(); questions.splice(Number(btn.dataset.qRm), 1); paint() }))
    ui.forms.bind(ctx, $('q-save'), async () => {
      if (guardRegistration($('q-save'))) return
      const btn = $('q-save'); btn.disabled = true
      collectQuestions()
      assignQuestionIds()
      try {
        const r = await api('PUT', path + '/registration', { questions, approval_required: $('signup-approval').checked })
        if (!r.ok) { setLive(ui.errText(r), 'err', btn); btn.disabled = false; return }
        questions = r.data.questions || questions
        // savedQuestions is cloned here, not aliased to `questions` -- the
        // draft array is about to keep being mutated by Add/Remove, and the
        // approval toggle below must only ever send what was last saved.
        savedQuestions = questions.map(q => ({ ...q }))
        approvalRequired = !!r.data.approval_required
        setLive('Questions saved.', 'ok', btn)
        paint()
      } catch (e) { setLive('Cannot reach the server.', 'err', btn); btn.disabled = false }
    }, { key: 'questions', fields: () => [...main.querySelectorAll('[data-q-prompt], [data-q-required]')], snapshot: () => questions, restore: value => { questions = value; paint() } })
    ui.forms.instant(ctx, $('signup-approval').closest('label'), { fields: () => [$('signup-approval')] })
    $('signup-approval').addEventListener('change', async () => {
      if (guardRegistration($('signup-approval'))) { $('signup-approval').checked = approvalRequired; return }
      const box = $('signup-approval'); const prev = !box.checked; box.disabled = true
      let admitPending = false
      // D5: admit_pending only ever admits join-link pending -- applications
      // always stay pending regardless of this toggle -- so only link
      // sign-ups count here, and the confirm below never mentions applications.
      const pendingNow = applications.filter(a => a.status === 'pending' && a.joined_via !== 'application')
      if (!box.checked && pendingNow.length) {
        // C12: turning approval off used to offer only Approve all / Leave
        // pending, with no way to back out of flipping the toggle at all.
        // ui.confirm is a fixed two-button shape, so this uses ui.dialog
        // directly for the real three-way choice.
        const choice = await ui.dialog({
          title: 'Waiting applications',
          body: (pendingNow.length === 1 ? '1 person who joined with your link is still waiting. Approve them now, or leave them pending. You can still approve or reject them later from this list.' : pendingNow.length + ' people who joined with your link are still waiting. Approve all of them now, or leave them pending. You can still approve or reject each one later from this list.') + ' Applications always wait for your decision.',
          buttons: [
            { label: 'Cancel', value: 'cancel', attrs: { 'data-cancel': '' } },
            { label: 'Leave them pending', value: 'leave' },
            { label: 'Approve all now', value: 'approve', className: 'btn primary' }
          ],
          escapeValue: 'cancel',
          focus: 0
        })
        if (choice === 'cancel') { box.checked = prev; box.disabled = false; return }
        admitPending = choice === 'approve'
      }
      try {
        const r = await api('PUT', path + '/registration', { questions: savedQuestions, approval_required: box.checked, admit_pending: admitPending })
        if (!r.ok) { box.checked = prev; setLive(ui.errText(r), 'err', box); box.disabled = false; return }
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
    })    }
    adminLock(main, admin, ev)
  }
  paint()
}
