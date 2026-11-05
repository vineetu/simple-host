// Shared "Tell participants?" dialog (SIM-341), extracted out of
// organiser-overview.js so organiser-results.js's Publish results can
// offer the exact same dialog -- same markup, suggestion wording,
// email-checkbox gating and live-region messages -- rather than growing
// its own near-copy that drifts from the original over time.
//
// Built on formDialog/ui.dialog (_orgA-util.js), never a new scrim --
// same pattern organiser-announcements.js's own Edit dialog uses.
import { formDialog } from './_orgA-util.js'
import { suggestedAnnouncement } from '../stage-announce.js'

// `ctx` is the screen's own render(ctx) context (main, api, ui, event,
// noAnnounce). `move` is a target stage id or the synthetic
// 'publish_results'; `back` is true only for a backward Change-stage
// move; `judgingLocked` says whether judging stays locked on a backward
// move to judging (SIM-341: the back-to-judging wording must not promise
// scoring reopens when it doesn't) -- unused for every other move.
// `emailableCount` and `setLive` are the caller's own
// organiser.counts.emailable_participants and ui.live(main), so this
// helper makes no assumption about how either screen builds its page.
export async function offerStageAnnouncement(ctx, { move, back, judgingLocked, emailableCount, setLive }) {
  // The organiser MCP App has no announcements route (its api() answers
  // "not available here"), so inside a chat the AI app makes this offer
  // itself, per hack_set_event_stage's description.
  if (ctx.noAnnounce) return
  const ui = ctx.ui
  const api = ctx.api.api
  const ev = ctx.event.event
  const suggestion = suggestedAnnouncement(move, { back, judgingLocked, eventTitle: ev.title })
  if (!suggestion) return
  const announcePath = '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/announcements'
  const body = document.createElement('div')
  body.innerHTML = `<p class="form-intro">Post an announcement about this change. You can edit it first.</p>
<div class="field"><label for="sa-title">Title</label><input class="input" id="sa-title" maxlength="120" value="${ui.esc(suggestion.title)}"></div>
<div class="field"><label for="sa-body">Message</label><textarea class="input" id="sa-body" rows="4" maxlength="3000">${ui.esc(suggestion.body)}</textarea></div>
<label class="check"><input type="checkbox" id="sa-email"${emailableCount ? '' : ' disabled'}><span><b>Email ${emailableCount} participants</b><span>${emailableCount ? 'Editing it later never sends another email.' : 'No participant has an email address yet.'}</span></span></label>`
  const q = sel => body.querySelector(sel)
  await formDialog(ui, { title: 'Tell participants?', body, saveLabel: 'Send', cancelLabel: "Don't send", focus: 'first-field' }, async () => {
    body.querySelectorAll('.err').forEach(n => n.remove())
    const t = q('#sa-title').value.trim(), b = q('#sa-body').value.trim()
    if (!t) { q('#sa-title').insertAdjacentHTML('afterend', '<p class="err">Title is required.</p>'); return false }
    if (!b) { q('#sa-body').insertAdjacentHTML('afterend', '<p class="err">Message is required.</p>'); return false }
    const emailChecked = q('#sa-email').checked
    try {
      const r = await api('POST', announcePath, { title: t, body: b, email_participants: emailChecked })
      if (!r.ok) { body.insertAdjacentHTML('beforeend', `<p class="err">${ui.esc(ui.errText(r))}</p>`); return false }
      // Same live-region wording organiser-announcements.js uses for the
      // same server response, so no two screens ever read differently
      // for the same result.
      if (!emailChecked) {
        setLive('Posted', 'ok')
      } else {
        const queued = r.data.emails_queued || 0
        if (queued === 0) {
          setLive('Posted. Nobody had an email address on file, so no email was sent.', 'err')
        } else {
          const copy = r.data.organiser_copied ? ' (including a copy to you)' : ''
          setLive(`Posted and emailed to ${queued} ${queued === 1 ? 'person' : 'people'}${copy}.`, 'ok')
        }
      }
      return true
    } catch (e) { body.insertAdjacentHTML('beforeend', '<p class="err">Cannot reach the server.</p>'); return false }
  })
  // 'Don't send', Escape and a scrim click all resolve formDialog false
  // with nothing posted -- there is no "nothing sent" message to show.
}
