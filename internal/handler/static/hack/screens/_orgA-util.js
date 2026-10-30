// Shared helpers for the organiser Overview / Event page / People / Settings
// / Create event screens and the public co-organiser invite (batch orgA).
// Not a shell module -- only these screens import it.
import { esc } from '../ui.js'

// fileAsDataURL reads a File into a data: URL (sponsor/FAQ logos travel as
// JSON, unlike the icon and website zip which go through apiBytes).
export function fileAsDataURL(file) {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result || ''))
    reader.onerror = () => reject(reader.error || new Error('could not read the file'))
    reader.readAsDataURL(file)
  })
}
export function fileAsArrayBuffer(file) {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(reader.result)
    reader.onerror = () => reject(reader.error || new Error('could not read the file'))
    reader.readAsArrayBuffer(file)
  })
}

// moveItem reorders arr in place, swapping i with i-1 (dir<0) or i+1 (dir>0).
export function moveItem(arr, i, dir) {
  const j = i + dir
  if (j < 0 || j >= arr.length) return
  const t = arr[i]; arr[i] = arr[j]; arr[j] = t
}

// adminLock disables every write control in main for the platform-admin
// read-only view (Copy buttons and the People role filter stay live), and
// shows the one banner every organiser screen carries in that state.
export function adminLock(main, admin) {
  if (!admin) return
  const banner = document.createElement('div')
  banner.className = 'note warn'
  banner.setAttribute('role', 'alert')
  banner.textContent = 'Viewing as platform admin. Everything here is read only.'
  main.insertBefore(banner, main.firstChild)
  main.querySelectorAll('button, input, textarea, select').forEach(n => {
    if (n.hasAttribute('data-admin-live')) return
    n.disabled = true
  })
}

// stagePicker opens a dialog with a <select> of every offered stage except
// the current one, defaulting to the next stage forward; resolves the chosen
// stage id or '' on cancel. Moving backward gets its own warning line, from
// the stage's own effect (features.json: change-event-stage).
// Kept as its own scrim rather than built on ui.dialog: it autofocuses the
// <select> itself (so arrow keys work immediately), where ui.dialog always
// focuses one of its buttons, and "Continue" has to resolve with whatever
// stage is currently selected, not a fixed per-button value.
const STAGE_ORDER = ['draft', 'open', 'building', 'closed', 'judging', 'results', 'archived']
const STAGE_LABEL = { draft: 'Draft', open: 'Open', building: 'Building', closed: 'Entries closed', judging: 'Judging', results: 'Results', archived: 'Ended' }
function backwardEffect(next) {
  if (next === 'draft') return 'People who joined stay, but the join link stops working until you open it again.'
  if (next === 'open' || next === 'building') return 'Entries open again. People who joined stay.'
  if (next === 'closed') return 'Entries reopen for teams with extra time. Other entries stay closed. People who joined stay.'
  return 'Judging opens again. Entries stay frozen and people who joined stay.'
}
export function stagePicker(current, offered) {
  return new Promise(resolve => {
    const order = STAGE_ORDER.filter(s => offered.includes(s) && s !== 'archived')
    const curIdx = order.indexOf(current)
    const choices = order.filter(s => s !== current)
    if (!choices.length) { resolve(''); return }
    const nextIdx = curIdx >= 0 && curIdx < order.length - 1 ? curIdx + 1 : choices.length - 1
    const scrim = document.createElement('div')
    scrim.className = 'scrim'
    scrim.innerHTML = `<div class="dialog" role="dialog" aria-modal="true" aria-labelledby="stage-title"><h2 id="stage-title">Change stage</h2><div class="field"><label>New stage<select class="select" id="stage-pick">${choices.map(s => `<option value="${esc(s)}"${s === order[nextIdx] ? ' selected' : ''}>${esc(STAGE_LABEL[s] || s)}</option>`).join('')}</select></label></div><p class="note warn" id="stage-warn" hidden></p><div class="acts"><button class="btn" data-cancel type="button">Cancel</button><button class="btn primary" data-confirm type="button">Continue</button></div></div>`
    const sel = scrim.querySelector('#stage-pick')
    const warn = scrim.querySelector('#stage-warn')
    function paint() {
      const target = sel.value
      const back = order.indexOf(target) < curIdx
      if (back) { warn.hidden = false; warn.textContent = backwardEffect(target) }
      else if (target === 'closed') { warn.hidden = false; warn.textContent = "Entries close for every team right now, except teams with their own extension." }
      else { warn.hidden = true }
    }
    sel.addEventListener('change', paint)
    paint()
    function done(value) { document.removeEventListener('keydown', keydown, true); scrim.remove(); resolve(value) }
    function keydown(e) { if (e.key === 'Escape') { e.preventDefault(); done('') } }
    scrim.querySelector('[data-cancel]').addEventListener('click', () => done(''))
    scrim.querySelector('[data-confirm]').addEventListener('click', () => done(sel.value))
    scrim.addEventListener('click', e => { if (e.target === scrim) done('') })
    document.addEventListener('keydown', keydown, true)
    document.body.append(scrim)
    sel.focus()
  })
}
