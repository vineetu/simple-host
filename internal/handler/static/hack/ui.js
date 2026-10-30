export const esc = value => String(value ?? '').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]))
export const stages = { draft: 'Not open yet', open: 'Sign-up open', building: 'Building', closed: 'Entries closed', judging: 'Judging', results: 'Results', archived: 'Ended', ended: 'Ended' }
export const stageLabel = stage => stages[stage] || stage || ''
export const roleLabel = role => ({organiser:'Organiser',participant:'Participant',judge:'Judge'}[role] || '')
export const initials = name => String(name || '').split(/\s+/).slice(0,2).map(s => s[0] || '').join('').toUpperCase()
export function classicURL() { const u = new URL(location.href); u.searchParams.set('ui', 'classic'); return u.pathname + u.search + u.hash }
export function dateTime(value, event, options = {}) {
  if (!value || Number.isNaN(new Date(value).getTime())) return ''
  return new Intl.DateTimeFormat(undefined, {dateStyle:'medium',timeStyle:'short', ...(event?.time_zone ? {timeZone:event.time_zone} : {}), ...options}).format(new Date(value))
}
export function nextDeadline(event) {
  return [['Starts', event?.starts_at], ['Entries close', event?.submission_deadline], ['Ends', event?.ends_at]]
    .filter(([, value]) => value && new Date(value) > new Date()).sort((a,b) => new Date(a[1]) - new Date(b[1]))[0]
}
export function toast(text, near, bad = false) {
  if (!text) return
  const note = document.createElement('p')
  note.className = 'note ' + (bad ? 'bad' : 'ok')
  note.setAttribute('role', bad ? 'alert' : 'status')
  note.textContent = text
  ;(near || document.querySelector('main') || document.getElementById('app')).append(note)
  setTimeout(() => note.remove(), 6000)
}
export const saved = near => toast('Saved', near)
export function copyButton(text, label = 'Copy') {
  const button = document.createElement('button')
  button.type = 'button'
  button.className = 'btn sm'
  button.textContent = label
  button.addEventListener('click', async () => {
    try { await navigator.clipboard.writeText(text); button.textContent = 'Copied'; setTimeout(() => button.textContent = label, 2000) }
    catch { toast('Could not copy. Select the text and copy it.', button.parentElement, true) }
  })
  return button
}
// In-page confirm dialog; screens call ui.confirm. Never the browser's native one.
export { ask as confirm }
function ask({ title, body, confirmLabel = 'Continue', cancelLabel = 'Cancel', danger = false }) {
  return new Promise(resolve => {
    const previous = document.activeElement
    const scrim = document.createElement('div')
    scrim.className = 'scrim'
    scrim.innerHTML = `<div class="dialog" role="dialog" aria-modal="true" aria-labelledby="confirm-title" aria-describedby="confirm-body"><h2 id="confirm-title">${esc(title)}</h2><p id="confirm-body">${esc(body)}</p><div class="acts"><button class="btn" data-cancel>${esc(cancelLabel)}</button><button class="btn ${danger ? 'danger' : 'primary'}" data-confirm>${esc(confirmLabel)}</button></div></div>`
    const buttons = [...scrim.querySelectorAll('button')]
    function done(value) { document.removeEventListener('keydown', keydown, true); scrim.remove(); previous?.focus(); resolve(value) }
    function keydown(e) {
      if (e.key === 'Escape') { e.preventDefault(); done(false) }
      if (e.key === 'Tab') {
        e.preventDefault()
        const index = buttons.indexOf(document.activeElement)
        buttons[(index + (e.shiftKey ? buttons.length - 1 : 1)) % buttons.length].focus()
      }
    }
    buttons[0].addEventListener('click', () => done(false))
    buttons[1].addEventListener('click', () => done(true))
    scrim.addEventListener('click', e => { if (e.target === scrim) done(false) })
    document.addEventListener('keydown', keydown, true)
    document.body.append(scrim)
    buttons[0].focus()
  })
}
