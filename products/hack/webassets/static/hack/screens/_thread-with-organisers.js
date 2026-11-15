// The participant's side of a team's thread with the organisers (SIM-443):
// the card on Team and the dialog that writes to the organisers. The rows are
// the organiser pages' own (messageRowsHTML).
import { formDialog } from './_orgA-util.js'
import { messageRowsHTML, MAX_TEXT_LEN } from './_team-message-dialog.js'

// participant-team.js's card: the team's thread with the organisers and a
// button to write to them (not once the event has ended). Hidden on an ended
// event with nothing in it.
export function organiserThreadCardHTML(ui, ev, messages) {
  const ended = ev.stage === 'archived'
  if (ended && (!messages || !messages.length)) return ''
  const rows = messageRowsHTML(ui, ev, messages) || '<p class="muted">No messages yet.</p>'
  return `<section class="card" id="organiser-thread"><h2>Messages with the organisers</h2>${rows}${ended ? '' : '<div class="form-actions"><button class="btn" type="button" id="msg-organisers-btn">Message the organisers</button></div>'}</section>`
}

// openMessageOrganisersDialog: a team member writes to the event's
// organisers -- POST .../team/messages {"text"}. Resolves the sent message,
// or null on cancel. Failures (400 invalid_text, 409 no_team/event_closed,
// 429 rate_limited) keep the dialog open with the server's own message.
export async function openMessageOrganisersDialog(ctx) {
  const { ui } = ctx
  const client = ctx.api
  const api = client.api
  const slug = ctx.event.event.slug
  const body = document.createElement('div')
  body.innerHTML = `<div class="field"><div class="lab-row"><label for="mo-text">Message</label><span class="counter" id="mo-ct">0 / ${MAX_TEXT_LEN}</span></div><textarea class="input" id="mo-text" rows="5" maxlength="${MAX_TEXT_LEN}"></textarea><p class="help">Every organiser can read it. Your teammates see it too.</p><p class="err" id="mo-err"></p></div>`
  const textarea = body.querySelector('#mo-text')
  const counter = body.querySelector('#mo-ct')
  const errEl = body.querySelector('#mo-err')
  textarea.addEventListener('input', () => { counter.textContent = textarea.value.length + ' / ' + MAX_TEXT_LEN })

  let sent = null
  const saved = await formDialog(ui, { title: 'Message the organisers', body, saveLabel: 'Send', focus: 'first-field' }, async () => {
    errEl.textContent = ''
    const text = textarea.value.trim()
    if (!text) { errEl.textContent = 'Write a message first.'; return false }
    let r
    try { r = await api('POST', '/v1/hack/events/' + encodeURIComponent(slug) + '/team/messages', { text }) }
    catch (e) { if (!client.isSignedOut(e)) errEl.textContent = 'Cannot reach the server.'; return false }
    if (!r.ok) { errEl.textContent = ui.errText(r, { fallback: 'Could not send the message.' }); return false }
    sent = r.data.message || null
    return true
  })
  if (!saved || !sent) return null
  return sent
}
