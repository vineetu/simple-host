// Shared helpers for the organiser Overview / Event page / People / Settings
// / Create event screens and the public co-organiser invite (batch orgA).
// Not a shell module -- only these screens import it.
import { esc, stageLabel } from '../ui.js'

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

// adminLock disables every write control in main and shows the one banner
// that explains why, for either case these settings-style screens (Event
// page, People) share: a platform-admin inspection, or -- passing the
// event as the third argument -- an archived event (S14: one note,
// "Settings can't change any more", not sprinkled per section). Copy
// buttons and the People role filter stay live either way (marked
// data-admin-live).
export function adminLock(main, admin, ev) {
  const archived = !admin && ev && ev.stage === 'archived'
  if (!admin && !archived) return
  const banner = document.createElement('div')
  banner.className = 'note warn'
  banner.setAttribute('role', 'alert')
  banner.textContent = admin
    ? 'Viewing as platform admin. Everything here is read only.'
    : "This event has ended. Settings can't change any more."
  main.insertBefore(banner, main.firstChild)
  main.querySelectorAll('button, input, textarea, select').forEach(n => {
    if (n.hasAttribute('data-admin-live')) return
    n.disabled = true
  })
}

// stagePicker opens a dialog with a <select> of every offered stage except
// the current one, defaulting to the next stage forward; resolves the chosen
// stage id or '' on cancel. Every choice shows a one-line summary of what
// changes for participants and judges (owner decision 2026-10-30: "every
// stage change confirms with a summary of what happens"), from FORWARD_EFFECT
// for a forward or lateral move, or the stage's own backward effect
// (features.json: change-event-stage).
// Kept as its own scrim rather than built on ui.dialog: it autofocuses the
// <select> itself (so arrow keys work immediately), where ui.dialog always
// focuses one of its buttons, and "Continue" has to resolve with whatever
// stage is currently selected, not a fixed per-button value.
const STAGE_ORDER = ['draft', 'open', 'building', 'closed', 'judging', 'results', 'archived']
function backwardEffect(next) {
  if (next === 'draft') return 'People who joined stay, but the join link stops working until you open it again.'
  if (next === 'open' || next === 'building') return 'Entries open again. People who joined stay.'
  if (next === 'closed') return 'Entries reopen for teams with extra time. Other entries stay closed. People who joined stay.'
  return 'Judging opens again. Entries stay frozen and people who joined stay.'
}
// What a forward (or lateral, since the picker offers every other stage, not
// only the next one) move to each stage means for participants and judges.
// Each line must match the server's own stage rules (hack_judging.go
// scoringStage: judges may score only at closed/judging, never results;
// hack.go submissionsClosedStage/joinable): closed is when scoring actually
// opens, judging is when it's mid-way, and results is when it stops.
const FORWARD_EFFECT = {
  open: 'Sign-up opens. Participants can join and apply, and judges can use their invite link.',
  building: 'Entries open. Teams can edit their entry and website until you close entries.',
  closed: 'Entries close for every team now, except teams with their own extension. Judges can start scoring.',
  judging: 'Teams can no longer edit their entry, team or website. Judges keep scoring until you lock judging.',
  results: 'Scoring and sign-up stop. Publish results when you are ready.'
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
    scrim.innerHTML = `<div class="dialog" role="dialog" aria-modal="true" aria-labelledby="stage-title"><h2 id="stage-title">Change stage</h2><div class="field"><label>New stage<select class="select" id="stage-pick">${choices.map(s => `<option value="${esc(s)}"${s === order[nextIdx] ? ' selected' : ''}>${esc(stageLabel(s))}</option>`).join('')}</select></label></div><p class="note warn" id="stage-warn" hidden></p><div class="acts"><button class="btn" data-cancel type="button">Cancel</button><button class="btn primary" data-confirm type="button">Continue</button></div></div>`
    const sel = scrim.querySelector('#stage-pick')
    const warn = scrim.querySelector('#stage-warn')
    function paint() {
      const target = sel.value
      const back = order.indexOf(target) < curIdx
      warn.hidden = false
      warn.textContent = back ? backwardEffect(target) : (FORWARD_EFFECT[target] || '')
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
