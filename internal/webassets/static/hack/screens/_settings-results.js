import { href } from '../nav.js'
export async function render(ctx, section) {
  const { main, ui, role } = ctx
  const client = ctx.api
  const api = client.api
  const ev = ctx.event.event
  const slug = ev.slug
  const base = '/v1/hack/events/' + encodeURIComponent(slug)
  const $ = id => main.querySelector('#' + id)
  const setLive = ui.live(main)
  const judgingLocked = !!ev.judging_locked_at
  // SIM-341: the same "Tell participants?" offer organiser-overview.js
  // makes after every stage move, here after Publish results -- same
  // emailable count source (organiser.counts) as that screen and
  // organiser-announcements.js's own "New announcement" form.
  const data = ctx.event
  const emailableCount = (data.organiser && data.organiser.counts && data.organiser.counts.emailable_participants) || 0
  // S14: the scoring/visibility/voting settings below don't change any
  // more once the event has ended (judging settings PATCH and voting PUT
  // both already refuse archived server-side) -- Publish, the Directory
  // toggle (its own PATCH has no archived check, so it's left live) and
  // the downloads stay as they were, since they're still useful after End.
  const archived = ev.stage === 'archived'
  const lockReason = archived ? "This event has ended. Settings can't change any more." : ''

  let settings = { score_mode: 'raw', public_scores: false, public_ranks: false }
  let results = null // array of rows once published, else null
  let preview = [] // current standings, used to build overrides before first publish
  let previewOk = false // #11: only true once /judging/preview actually loaded -- a failed read must not be read as "every published team is now missing"
  let fullRanking = false
  let voting = { enabled: false, opens_at: null, closes_at: null, eligibility: 'all_signed_in', directory_listed: false, status: 'disabled', votes_to_remove: {} }

  main.innerHTML = '<p class="note">Loading…</p>'

  try {
    const [settingsR, resultsR, votingR, previewR] = await Promise.all([
      api('GET', base + '/judging/settings'),
      api('GET', base + '/results'),
      api('GET', base + '/voting'),
      api('GET', base + '/judging/preview'),
    ])
    if (settingsR.ok) settings = settingsR.data || settings
    if (resultsR.ok) {
      results = (resultsR.data && resultsR.data.results) || (Array.isArray(resultsR.data) ? resultsR.data : null)
      fullRanking = !!(resultsR.data && resultsR.data.full_ranking)
    }
    if (votingR.ok) voting = votingR.data || voting
    if (previewR.ok) { preview = (previewR.data && previewR.data.results) || []; previewOk = true }
  } catch (e) {
    if (!client.isSignedOut(e)) setLive('Cannot reach the server.', 'err')
    return
  }

 if (!ctx.alive()) return
 main.innerHTML = section === 'directory' ? `<section class="card" id="directory"><h2>Directory</h2>
<label class="check"><input type="checkbox" id="res-directory-listed"><span>List this event on Find a hackathon</span></label>
<div class="form-actions"><button class="btn" type="button" id="res-directory-save">Save</button></div>
</section>
` : `<section class="card" id="ranking"><h2>How teams are ranked</h2><div class="field"><label>Score used for ranking<select class="select" id="res-score-mode"${archived ? ' disabled' : ''}><option value="normalised">Average, adjusted for strict and generous judges</option><option value="raw">Average score</option></select></label></div><div class="form-actions"><button class="btn" type="button" id="res-score-mode-save"${archived ? ' disabled' : ''}>Save</button></div></section>
<section class="card"><h2>Show on the public page</h2><p class="sub">Tip: Full ranking with scores off lists every team without a scoreboard.</p><div class="field"><label>Visibility<select class="select" id="res-visibility"${archived ? ' disabled' : ''}><option value="true">Full ranking</option><option value="false">Winners only</option></select></label></div><label class="check"><input type="checkbox" id="res-public-scores"${archived ? ' disabled' : ''}><span><b>Show scores</b><span>Teams always see their own score.</span></span></label><label class="check"><input type="checkbox" id="res-public-ranks"${archived ? ' disabled' : ''}><span>Show ranks</span></label><div class="form-actions"><button class="btn" type="button" id="res-visibility-save"${archived ? ' disabled' : ''}>Save</button><span class="hint" id="res-visibility-hint" hidden>Publish results first to control what the public page shows.</span></div></section>
<section class="card" id="voting"><h2>People’s choice</h2>
<p><span class="pill" id="res-vote-status"></span></p>
<div id="voting-window"><label class="check"><input type="checkbox" id="res-vote-enabled"${archived ? ' disabled' : ''}><span>Let visitors vote</span></label>
<div class="field-row"><div class="field"><label>Starts · <span id="res-tz-1"></span><input class="input" type="datetime-local" id="res-vote-opens"${archived ? ' disabled' : ''}></label></div><div class="field"><label>Ends · <span id="res-tz-2"></span><input class="input" type="datetime-local" id="res-vote-closes"${archived ? ' disabled' : ''}></label></div></div>
<div class="form-actions"><button class="btn" type="button" id="res-vote-window-save"${archived ? ' disabled' : ''}>Save voting window</button></div>
</div><hr class="rule">
<div id="voting-eligibility">
<div class="field"><label>Who can vote<select class="select" id="res-vote-eligibility"${archived ? ' disabled' : ''}><option value="all_signed_in">Anyone signed in</option><option value="event_members">Approved event members</option><option value="participants">Approved participants only</option></select></label></div>
<div class="form-actions"><button class="btn" type="button" id="res-vote-eligibility-save"${archived ? ' disabled' : ''}>Save</button></div></div>
</section>`
 if (section !== 'directory') {
  // --- scoring rules ------------------------------------------------------
  $('res-score-mode').value = settings.score_mode || 'raw'
  $('res-score-mode-save').addEventListener('click', async () => {
    const btn = $('res-score-mode-save')
    btn.disabled = true
    let r
    const sent = rankingForm.snapshot()
    try { r = await api('PATCH', base + '/judging/settings', { score_mode: $('res-score-mode').value }) }
    catch (e) { if (!client.isSignedOut(e)) setLive('Cannot reach the server.', 'err', btn); btn.disabled = false; return }
    btn.disabled = false
    if (!r.ok) { setLive(ui.errText(r), 'err', btn); return }
    const editedDuring = rankingForm.snapshot() !== sent
    settings.score_mode = (r.data && r.data.score_mode) || $('res-score-mode').value
    editedDuring ? rankingForm.reset(sent) : rankingForm.reset()
    setLive('Saved', 'ok', btn)
  })


 }
  // --- visibility ---------------------------------------------------------
  function paintVisibility() {
    if (!$('res-visibility')) return
    $('res-visibility').value = String(fullRanking)
    $('res-public-scores').checked = !!settings.public_scores
    $('res-public-ranks').checked = !!settings.public_ranks
    $('res-visibility').disabled = archived || !results
    $('res-visibility-hint').hidden = !!results || archived
  }
  paintVisibility()
  $('res-visibility-save')?.addEventListener('click', async () => {
    const btn = $('res-visibility-save')
    btn.disabled = true
    let ok = true
    let r1
    const sent = visibilityForm.snapshot()
    try { r1 = await api('PATCH', base + '/judging/settings', { public_scores: $('res-public-scores').checked, public_ranks: $('res-public-ranks').checked }) }
    catch (e) { if (!client.isSignedOut(e)) setLive('Cannot reach the server.', 'err', btn); btn.disabled = false; return }
    if (!r1.ok) { setLive(ui.errText(r1), 'err', btn); btn.disabled = false; return }
    settings.public_scores = $('res-public-scores').checked
    settings.public_ranks = $('res-public-ranks').checked
    if (results) {
      let r2
      try { r2 = await api('PATCH', base + '/results', { full_ranking: $('res-visibility').value === 'true' }) }
      catch (e) { if (!client.isSignedOut(e)) setLive('Cannot reach the server.', 'err', btn); btn.disabled = false; return }
      if (!r2.ok) { ok = false; setLive(ui.errText(r2), 'err', btn) }
      else fullRanking = !!(r2.data && r2.data.full_ranking)
    }
    btn.disabled = false
    const editedDuring = visibilityForm.snapshot() !== sent
    if (ok) editedDuring ? visibilityForm.reset(sent) : visibilityForm.reset()
    if (ok) setLive('Saved', 'ok', btn)
  })

  // --- voting & directory -------------------------------------------------
  if ($('res-tz-1')) $('res-tz-1').textContent = $('res-tz-2').textContent = ev.time_zone || 'UTC'
  // paintVoting only touches the voting-window fields: the directory
  // checkbox has its own save button and its own paint (paintDirectory), so
  // an unrelated voting save never overwrites a directory choice still
  // sitting unsaved in the checkbox.
  function paintVoting(part) {
    if (!$('res-vote-status')) return
    const labels = { open: 'Voting open', upcoming: 'Voting upcoming', closed: 'Voting closed', disabled: 'Voting off' }
    const pill = $('res-vote-status')
    pill.textContent = labels[voting.status] || labels.disabled
    pill.className = 'pill' + (voting.status === 'open' ? ' ok' : voting.status === 'disabled' ? '' : ' warn')
    if (!part || part === 'window') {
      $('res-vote-enabled').checked = !!voting.enabled
      $('res-vote-opens').value = ui.localInputValue(voting.opens_at, ev.time_zone)
      $('res-vote-closes').value = ui.localInputValue(voting.closes_at, ev.time_zone)
    }
    if (!part || part === 'eligibility') $('res-vote-eligibility').value = voting.eligibility || 'all_signed_in'
  }
  function paintDirectory() {
    if (!$('res-directory-listed')) return
    $('res-directory-listed').checked = !!voting.directory_listed
  }
  paintVoting()
  paintDirectory()

  // The gallery-closed refusal (409 gallery_closed) is common to both
  // voting-window and eligibility saves (either can re-send `enabled: true`
  // once the gallery has since been closed) -- handled once here instead of
  // per caller, with a readable note and a link to where the fix lives,
  // instead of the server's plain sentence.
  function clearGalleryNote() {
    const existing = $('res-vote-gallery-note')
    if (existing) existing.remove()
  }
  function showGalleryNote(btn) {
    clearGalleryNote()
    const p = document.createElement('p')
    p.id = 'res-vote-gallery-note'
    p.className = 'note bad'
    p.setAttribute('role', 'alert')
    // #12: match organiser-event-page.js's actual heading ("Look") and
    // toggle copy ("Show entries on the event page") -- the section itself
    // has been renamed since this note was first written, which left it
    // naming a heading ("Gallery & appearance") that no longer exists.
    p.append(document.createTextNode('Turn on Show entries on the event page first, on '))
    const a = document.createElement('a')
    a.href = href(role, slug, 'event-page') + '#appearance'
    a.textContent = 'Event page → Look'
    p.append(a, document.createTextNode('.'))
    btn.insertAdjacentElement('afterend', p)
  }

  // Handles the server's authoritative vote_removal_confirmation 409 itself
  // (a count fetched ahead of time can go stale if a vote arrives in
  // between), so every caller gets the confirm-and-retry regardless of
  // which field changed.
  async function putVoting(body, btn) {
    clearGalleryNote()
    btn.disabled = true
    let r
    try { r = await api('PUT', base + '/voting', body) }
    catch (e) { if (!client.isSignedOut(e)) setLive('Cannot reach the server.', 'err', btn); btn.disabled = false; return null }
    if (!r.ok && r.data && r.data.code === 'gallery_closed') {
      btn.disabled = false
      showGalleryNote(btn)
      return null
    }
    if (!r.ok && r.data && r.data.code === 'vote_removal_confirmation') {
      const count = r.data.votes_to_remove || 0
      const ok = await ui.confirm({ title: 'Remove earlier votes?', body: 'This removes ' + count + (count === 1 ? ' vote that no longer qualifies' : ' votes that no longer qualify') + ". This can't be undone. Continue?", confirmLabel: 'Save and remove votes', danger: true })
      if (!ok) { btn.disabled = false; return null }
      try { r = await api('PUT', base + '/voting', { ...body, confirm_vote_removal: true }) }
      catch (e) { if (!client.isSignedOut(e)) setLive('Cannot reach the server.', 'err', btn); btn.disabled = false; return null }
    }
    btn.disabled = false
    if (!r.ok) { setLive(ui.errText(r), 'err', btn); return null }
    return r
  }

  function applyVotingSave(data) {
    // directory_listed is excluded: the voting endpoint doesn't own that
    // setting, and merging its (possibly stale) echo back in would repaint
    // over an unsaved directory choice.
    const { directory_listed, ...rest } = data || {}
    voting = { ...voting, ...rest }
  }

  $('res-vote-window-save')?.addEventListener('click', async () => {
    const btn = $('res-vote-window-save')
    const sent = votingWindowForm.snapshot()
    const r = await putVoting({
      enabled: $('res-vote-enabled').checked,
      opens_at: $('res-vote-opens').value,
      closes_at: $('res-vote-closes').value,
      eligibility: voting.eligibility,
    }, btn)
    if (!r) return
    const editedDuring = votingWindowForm.snapshot() !== sent
    applyVotingSave(r.data)
    paintVoting('window')
    editedDuring ? votingWindowForm.reset(sent) : votingWindowForm.reset()
    setLive(r.data.votes_removed ? r.data.votes_removed + (r.data.votes_removed === 1 ? ' earlier vote' : ' earlier votes') + ' removed' : 'Saved', 'ok', btn)
  })

  $('res-vote-eligibility-save')?.addEventListener('click', async () => {
    const btn = $('res-vote-eligibility-save')
    const newEligibility = $('res-vote-eligibility').value
    const sent = votingEligibilityForm.snapshot()
    const r = await putVoting({
      enabled: voting.enabled,
      opens_at: voting.opens_at,
      closes_at: voting.closes_at,
      eligibility: newEligibility,
    }, btn)
    if (!r) return
    const editedDuring = votingEligibilityForm.snapshot() !== sent
    applyVotingSave(r.data)
    paintVoting('eligibility')
    editedDuring ? votingEligibilityForm.reset(sent) : votingEligibilityForm.reset()
    setLive(r.data.votes_removed ? r.data.votes_removed + (r.data.votes_removed === 1 ? ' earlier vote' : ' earlier votes') + ' removed' : 'Saved', 'ok', btn)
  })

  $('res-directory-save')?.addEventListener('click', async () => {
    const btn = $('res-directory-save')
    btn.disabled = true
    let r
    const sent = directoryForm.snapshot()
    try { r = await api('PATCH', base + '/directory', { listed: $('res-directory-listed').checked }) }
    catch (e) { if (!client.isSignedOut(e)) setLive('Cannot reach the server.', 'err', btn); btn.disabled = false; return }
    btn.disabled = false
    if (!r.ok) { setLive(ui.errText(r), 'err', btn); return }
    const editedDuring = directoryForm.snapshot() !== sent
    voting.directory_listed = !!(r.data && r.data.listed)
    editedDuring ? directoryForm.reset(sent) : directoryForm.reset()
    setLive('Saved', 'ok', btn)
  })

  const rankingForm = $('ranking') ? ui.trackForm(ctx, $('ranking')) : null
  const visibilityForm = $('res-visibility') ? ui.trackForm(ctx, $('res-visibility').closest('section')) : null
  const votingWindowForm = $('voting-window') ? ui.trackForm(ctx, $('voting-window')) : null
  const votingEligibilityForm = $('voting-eligibility') ? ui.trackForm(ctx, $('voting-eligibility')) : null
  const directoryForm = $('directory') ? ui.trackForm(ctx, $('directory')) : null


}
