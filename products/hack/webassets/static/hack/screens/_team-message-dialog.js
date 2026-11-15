// Team messages (notices contract 2026-11-05, "team_message"): the Send
// dialog and the sent-messages list rendering, shared by organiser-team-
// detail.js (one team), organiser-teams.js (up to 25, from row checkboxes)
// and participant-team.js (read-only list, no dialog). One module so the
// textarea/counter/Send behaviour and the "show the server message" error
// text (contract: 429 rate_limited) can't drift between the two organiser
// screens that open it. Named like the `_orgA-util.js`/`_partB-util.js`
// screen-util files, but at the top level since it is shared across an
// organiser and a participant screen, not scoped to either batch.
import { formDialog } from './_orgA-util.js'

export const MAX_TEXT_LEN = 1000

// "3 hours ago" -- the same shape as participant-website.js's own agoText
// (that module is participant-only, so this is a small duplicate rather
// than a new shared export out of it).
export function agoText(iso) {
  if (!iso) return ''
  const diff = Date.now() - new Date(iso).getTime()
  if (!Number.isFinite(diff) || diff < 0) return ''
  if (diff < 60000) return 'just now'
  const m = Math.floor(diff / 60000)
  if (m < 60) return m + ' minute' + (m === 1 ? '' : 's') + ' ago'
  const h = Math.floor(m / 60)
  if (h < 24) return h + ' hour' + (h === 1 ? '' : 's') + ' ago'
  const d = Math.floor(h / 24)
  return d + ' day' + (d === 1 ? '' : 's') + ' ago'
}

// The rows for a team's thread, newest first (as the API already returns
// them) -- "<sender> (team|organiser) · <relative> · <absolute>". The thread
// holds both directions (SIM-443), so each row says which side wrote it.
// '' when there are none -- callers write their own empty state next to
// their own Message button.
export function messageRowsHTML(ui, ev, messages) {
  const esc = ui.esc
  if (!messages || !messages.length) return ''
  const rows = messages.map(m => {
    const abs = esc(ui.dateTime(m.created_at, ev))
    const rel = agoText(m.created_at)
    const when = rel ? `${esc(rel)} · ${abs}` : abs
    const meta = `${esc(m.sender_name || (m.from_team ? 'A teammate' : 'An organiser'))} (${m.from_team ? 'team' : 'organiser'}) · ${when}`
    // Its own class, not `<b>`: `.list .r .main b` is nowrap/ellipsis (built
    // for a one-line title), which clipped a long message body instead of
    // wrapping it (point 7). `.msg-text` (kit.css) wraps fully.
    return `<div class="r"><div class="main"><b class="msg-text">${esc(m.text)}</b><span>${meta}</span></div></div>`
  }).join('')
  return `<div class="list">${rows}</div>`
}

// openTeamMessageDialog: textarea (1000 max, live counter), Send ->
// POST .../team-messages {"team_ids": teamIds, "text"}. Resolves the
// response's `messages` array (one row per team actually messaged, each
// with its own `recipients` count) on Send, or null on cancel. A failed
// Send (400 invalid_team/invalid_text, 409 event_closed, 429 rate_limited)
// keeps the dialog open with the server's own message, through formDialog's
// existing retry loop -- "show the server message" is just letting
// ui.errText's default (no `codes` override) win.
export async function openTeamMessageDialog(ctx, { teamIds, title }) {
  const { ui } = ctx
  const client = ctx.api
  const api = client.api
  const slug = ctx.event.event.slug
  const body = document.createElement('div')
  body.innerHTML = `<div class="field"><div class="lab-row"><label for="tm-text">Message</label><span class="counter" id="tm-ct">0 / ${MAX_TEXT_LEN}</span></div><textarea class="input" id="tm-text" rows="5" maxlength="${MAX_TEXT_LEN}"></textarea><p class="help">Shows on the team's Overview. Teammates with email updates on also get an email.</p><p class="err" id="tm-err"></p></div>`
  const textarea = body.querySelector('#tm-text')
  const counter = body.querySelector('#tm-ct')
  const errEl = body.querySelector('#tm-err')
  textarea.addEventListener('input', () => { counter.textContent = textarea.value.length + ' / ' + MAX_TEXT_LEN })

  let sent = null
  const saved = await formDialog(ui, { title, body, saveLabel: 'Send', focus: 'first-field' }, async () => {
    errEl.textContent = ''
    const text = textarea.value.trim()
    if (!text) { errEl.textContent = 'Write a message first.'; return false }
    let r
    try { r = await api('POST', '/v1/hack/events/' + encodeURIComponent(slug) + '/team-messages', { team_ids: teamIds, text }) }
    catch (e) { if (!client.isSignedOut(e)) errEl.textContent = 'Cannot reach the server.'; return false }
    if (!r.ok) { errEl.textContent = ui.errText(r, { fallback: 'Could not send the message.' }); return false }
    sent = r.data.messages || []
    return true
  })
  if (!saved || !sent) return null
  return sent
}
