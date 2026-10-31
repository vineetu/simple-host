// Shared helpers for the organiser Overview / Event page / Announcements /
// People / Settings / Create event screens and the public co-organiser
// invite (batch orgA). Not a shell module -- only these screens import it.
// (The Basics/About unsaved-changes guard used to live here too, but
// app.js's popstate handler needs to call into it -- see R8/R9 -- and
// app.js only ever imports the shared ui.js, never a screen-only util, so
// setDirtyGuard/clearDirtyGuard/confirmDiscardForLeave moved there.)
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
    const previous = document.activeElement
    function done(value) { document.removeEventListener('keydown', keydown, true); scrim.remove(); if (previous && previous.focus) previous.focus(); resolve(value) }
    function keydown(e) {
      if (e.key === 'Escape') { e.preventDefault(); done(''); return }
      if (e.key === 'Tab') {
        // Keep focus inside the dialog, like every other dialog in the app.
        const items = [...scrim.querySelectorAll('select,button')].filter(el => !el.disabled)
        const i = items.indexOf(document.activeElement)
        e.preventDefault()
        items[(i + (e.shiftKey ? items.length - 1 : 1) + items.length) % items.length].focus()
      }
    }
    scrim.querySelector('[data-cancel]').addEventListener('click', () => done(''))
    scrim.querySelector('[data-confirm]').addEventListener('click', () => done(sel.value))
    scrim.addEventListener('click', e => { if (e.target === scrim) done('') })
    document.addEventListener('keydown', keydown, true)
    document.body.append(scrim)
    sel.focus()
  })
}

// ---------------------------------------------------------------------
// Atlas 2026-10-30 (Event page redesign): shared pieces for the new
// sub-page editors (organiser-event-page.js) and the new Announcements
// screen (organiser-announcements.js). Built on top of ui.dialog/ui.toast
// rather than beside them -- see each helper's own note.
// ---------------------------------------------------------------------

// formDialog drives an Add/Edit dialog built on ui.dialog (the shared
// scrim/focus-trap/Escape primitive -- never reimplemented here). `body` is
// a Node built once by the caller and reused across retries, so a rejected
// Save (onSave returns false after writing its own inline error into body)
// keeps what was typed and shows the error; ui.dialog itself always closes
// on any button click, so a retry re-opens it with the same body node.
// Resolves true if onSave eventually returned true, false if the person
// cancelled or pressed Escape.
export async function formDialog(ui, { title, body, saveLabel = 'Save', cancelLabel = 'Cancel', focus = 'first-field' }, onSave) {
  for (;;) {
    const action = await ui.dialog({
      title, body,
      buttons: [
        { label: cancelLabel, value: 'cancel', attrs: { 'data-cancel': '' } },
        { label: saveLabel, value: 'save', className: 'btn primary' }
      ],
      escapeValue: 'cancel',
      focus
    })
    if (action !== 'save') return false
    if (await onSave()) return true
  }
}

// rowMenuHTML: the icon-btn (...) plus its dropdown, same markup/behaviour as
// every atlas list row (Schedule, Sponsors, FAQ, Announcements). `items` is
// [{ label, act, danger, disabled }] in menu order; a danger item gets a
// <hr> above it automatically. `label` is the row's own name, for the
// aria-labels ("More for Kopi Kode").
export function rowMenuHTML(label, items) {
  let sawDanger = false
  const body = items.map(it => {
    const hr = it.danger && !sawDanger ? '<hr>' : ''
    if (it.danger) sawDanger = true
    return hr + `<button type="button" role="menuitem"${it.disabled ? ' disabled' : ''}${it.danger ? ' class="danger"' : ''} data-act="${esc(it.act)}">${esc(it.label)}</button>`
  }).join('')
  return `<button class="icon-btn" type="button" data-rowmenu aria-haspopup="menu" aria-expanded="false" aria-label="More for ${esc(label)}"><svg class="ico" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="2.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M5 10h.01M10 10h.01M15 10h.01"/></svg></button><div class="row-menu" role="menu" hidden>${body}</div>`
}

// wireRowMenus binds the open/close/Escape behaviour for every .row-menu
// currently in the document -- the same single global listener the atlas
// prototype's shell script uses (open one at a time, Escape or an outside
// click closes it). Bound once no matter how many screens or repaints call
// it (module-level flag, like shell.js's own bindResizeClose).
let rowMenuBound = false
export function wireRowMenus() {
  if (rowMenuBound) return
  rowMenuBound = true
  function closeAll(except) {
    document.querySelectorAll('.row-menu').forEach(m => {
      if (m === except) return
      m.hidden = true
      const b = m.parentNode && m.parentNode.querySelector('[data-rowmenu]')
      if (b) b.setAttribute('aria-expanded', 'false')
    })
  }
  document.addEventListener('click', e => {
    const b = e.target.closest('[data-rowmenu]')
    // R17: a click on one of the menu's own items (Edit, Move up, Remove...)
    // used to leave the menu open under whatever re-rendered next, since
    // only an outside click or [data-rowmenu] itself closed it.
    const item = e.target.closest('.row-menu button')
    if (item) {
      const m = item.closest('.row-menu')
      m.hidden = true
      const btn = m.parentNode && m.parentNode.querySelector('[data-rowmenu]')
      if (btn) btn.setAttribute('aria-expanded', 'false')
      return
    }
    if (!b) { if (!e.target.closest('.row-menu')) closeAll(null); return }
    const m = b.parentNode.querySelector('.row-menu')
    closeAll(m)
    m.hidden = !m.hidden
    b.setAttribute('aria-expanded', String(!m.hidden))
    if (!m.hidden) { const f = m.querySelector('button:not([disabled])'); if (f) f.focus() }
  })
  // R17: arrow-key navigation inside an open row menu (Up/Down/Home/End),
  // same as any other menu role -- Escape already closes it below.
  document.addEventListener('keydown', e => {
    if (e.key === 'Escape') {
      document.querySelectorAll('.row-menu:not([hidden])').forEach(m => {
        m.hidden = true
        const b = m.parentNode && m.parentNode.querySelector('[data-rowmenu]')
        if (b) { b.setAttribute('aria-expanded', 'false'); b.focus() }
      })
      return
    }
    if (!['ArrowUp', 'ArrowDown', 'Home', 'End'].includes(e.key)) return
    const menu = e.target.closest && e.target.closest('.row-menu:not([hidden])')
    if (!menu) return
    const items = Array.from(menu.querySelectorAll('button:not([disabled])'))
    if (!items.length) return
    e.preventDefault()
    const cur = items.indexOf(document.activeElement)
    let next
    if (e.key === 'Home') next = 0
    else if (e.key === 'End') next = items.length - 1
    else if (e.key === 'ArrowDown') next = cur === -1 ? 0 : (cur + 1) % items.length
    else next = cur === -1 ? items.length - 1 : (cur - 1 + items.length) % items.length
    items[next].focus()
  })
}

// undoToast: the "Removed <name>" toast with its own Undo button, for a
// Remove that writes straight away (Schedule/Sponsors/FAQ rows -- never for
// deleting an announcement, which keeps a real confirm instead). Not built
// on ui.toast, which has no second button; placed at the top of the given
// section (ui.js's own placement logic for a toast near a now-gone row is
// private to that module). Undo fires `undo` and removes the note; after
// `ms` (10s per the atlas) the note just disappears, no toast replaces it.
export function undoToast(section, text, undo, ms = 10000) {
  section.querySelectorAll('[data-undo-toast]').forEach(n => n.remove())
  const note = document.createElement('p')
  note.className = 'note ok'
  note.setAttribute('data-undo-toast', '')
  note.setAttribute('role', 'status')
  const span = document.createElement('span')
  span.textContent = text + ' '
  note.append(span)
  const btn = document.createElement('button')
  btn.type = 'button'
  btn.className = 'btn quiet sm'
  btn.textContent = 'Undo'
  let done = false
  const timer = setTimeout(() => { if (!done) { done = true; note.remove() } }, ms)
  btn.addEventListener('click', () => {
    if (done) return
    done = true
    clearTimeout(timer)
    note.remove()
    undo()
  })
  note.append(btn)
  const head = section.querySelector('.sp-head')
  if (head) head.insertAdjacentElement('afterend', note)
  else section.insertBefore(note, section.firstChild)
}

// The Basics/About unsaved-changes guard (setDirtyGuard/clearDirtyGuard/
// confirmDiscardForLeave) moved to ui.js -- see the note at the top of this
// file.
