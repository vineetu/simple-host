// One form lifecycle for screens and dialogs. Screen callbacks still own their API writes.
import { dialog } from './ui.js'
import { onSubmit } from './platform.js'

export const controls = 'input:not([type=button]):not([type=submit]):not([type=reset]):not([readonly]), textarea:not([readonly]), select, [contenteditable=true]'
export function values(fields) {
  return Array.from(fields, field => field.type === 'checkbox' || field.type === 'radio'
    ? field.checked : field.type === 'file' ? Array.from(field.files || [], f => [f.name, f.size, f.lastModified])
      : field.isContentEditable ? field.textContent : field.value)
}
export const snapshot = value => JSON.stringify(value)
export const changed = (before, value) => before !== snapshot(value)
export function errorField(code = '') {
  if (['invalid_name', 'name_taken', 'name_reserved'].includes(code)) return 'slug'
  return code.replace(/^(invalid_|missing_)/, '').replace(/_(taken|reserved|required|mismatch|too_long)$/, '')
}
// Kept independent of the DOM so status replacement/error expiry have small unit tests.
export function state() {
  let status = '', bad = false
  const errors = new Map()
  return {
    get status() { return status }, get bad() { return bad }, errors,
    message(text, error = false) { status = text || ''; bad = error },
    error(field, text) { errors.set(field, text); status = ''; bad = true },
    edit(field) { errors.delete(field); status = ''; bad = errors.size > 0 },
    clear() { errors.clear(); status = ''; bad = false },
    saved(text = 'Saved') { errors.clear(); status = text; bad = false }
  }
}
const drafts = new WeakMap()
let handles = new Set(), saving = new Set(), bound = false, leaving = null
let currentContext = null, initiating = null
export const requestOwner = () => initiating
export function context(ctx) { currentContext = ctx; active() }
function active() {
  for (const h of handles) if (!h.alive()) { h.expire(); h.destroy(); handles.delete(h) }
  return [...handles]
}
export function forElement(node) {
  const list = active()
  const exact = list.find(h => h.owns(node))
  if (exact) return exact
  const contained = list.filter(h => node && h.root.contains(node)).sort((a, b) => a.root.contains(b.root) ? 1 : -1)
  if (contained.length && (contained.length === 1 || contained[0].root !== contained[1].root)) return contained[0]
  if (initiating && (!node || node.contains?.(initiating.root))) return initiating
  const running = [...saving].filter(h => !node || h.ctx?.main === node || h.root.contains(node) || node.contains?.(h.root))
  if (running.length === 1) return running[0]
  return (node && list.find(h => h.owns(document.activeElement) && node.contains(h.root)))
    || (list.length === 1 && node?.contains?.(list[0].root) ? list[0] : null)
}
export function message(text, near, bad = false) {
  const h = forElement(near)
  if (!h) return false
  if (bad) h.error(text, near?.matches?.(controls) ? near : null)
  else { h.clearErrors(); h.status(text) }
  return true
}
// API responses identify fields by code/field/missing when the server supplies them.
export function response(result, owner) {
  if (result.ok || !owner) return
  owner.serverError(result)
}
export function register(ctx, options) {
  const { root, save, discard, label = root.querySelector('h2, legend')?.textContent || 'this form', saveButton, discardButton, hint, dirty = true } = options
  // A repaint detaches the old registration, even when its route ctx is still alive.
  active()
  const getFields = () => typeof options.fields === 'function' ? options.fields() : options.fields ? Array.from(options.fields) : [...root.querySelectorAll(controls)]
  const read = () => options.snapshot ? options.snapshot() : values(getFields())
  let store = ctx && drafts.get(ctx)
  if (ctx && !store) { store = new Map(); drafts.set(ctx, store) }
  const key = options.key || saveButton?.id || root.id || root.querySelector('[id]')?.id || (saveButton && 'action:' + saveButton.textContent.trim())
  const record = key && store?.get(key) || { baseline: snapshot(read()) }
  if (key) {
    store?.set(key, record)
    for (const old of handles) if (old.ctx === ctx && old.key === key) old.destroy()
  }
  let revision = 0, sentRevision = 0, sentSnapshot = null
  let initialFields = getFields(), initialValues = values(initialFields), busy = null
  const model = state(), marks = new Map()
  const statusSlot = document.createElement('span')
  statusSlot.className = 'hint form-status'; statusSlot.setAttribute('aria-live', 'polite'); statusSlot.dataset.formStatus = ''
  let dirtySlot = hint
  if (dirty && !dirtySlot) { dirtySlot = document.createElement('span'); dirtySlot.className = 'hint'; dirtySlot.dataset.formDirty = '' }
  const actions = saveButton?.closest('.card-foot, .form-actions, .sticky-foot, .acts, .control-row') || saveButton?.parentElement || root
  if (dirtySlot && !dirtySlot.isConnected) actions.append(dirtySlot)
  actions.append(statusSlot)
  function paint() {
    const isDirty = h.dirty()
    if (dirtySlot) { dirtySlot.textContent = isDirty ? 'Unsaved changes' : 'No unsaved changes'; dirtySlot.classList.toggle('dirty', isDirty) }
    if (discardButton) { discardButton.disabled = !isDirty; discardButton.hidden = !isDirty }
    if (saveButton && options.disableClean) saveButton.disabled = !!busy || !isDirty
    statusSlot.textContent = model.status; statusSlot.classList.toggle('err', model.bad)
  }
  function resolve(field) {
    if (typeof field !== 'string') return field
    field = options.aliases?.[field] || field
    const fields = getFields()
    const exact = fields.find(n => n.dataset.formField === field || n.name === field || n.id === field || n.id.endsWith('-' + field) || n.dataset.field === field)
      || root.querySelector(`[data-form-field="${CSS.escape(field)}"]`)
    if (exact) return exact
    const words = field.toLowerCase().split('_')
    const candidates = fields.map(n => {
      const tokens = (n.id + ' ' + n.name + ' ' + (n.labels?.[0]?.textContent || '') + ' ' + (n.getAttribute('aria-label') || '')).toLowerCase().split(/[^a-z0-9]+/)
      return [n, words.filter(word => tokens.includes(word)).length]
    }).filter(([, score]) => score > 0).sort((a, b) => b[1] - a[1])
    return candidates[0]?.[1] > (candidates[1]?.[1] || 0) ? candidates[0][0] : undefined

  }
  function clearMark(field) {
    const mark = marks.get(field)
    if (!mark) return
    const { line, holder, described } = mark
    field.removeAttribute('aria-invalid')
    if (described) field.setAttribute('aria-describedby', described); else field.removeAttribute('aria-describedby')
    line.remove(); holder?.classList.remove('has-error'); marks.delete(field)
  }
  const h = {
    root, label, model, ctx, key,
    replacement: null,
    expire: () => options.onExpire?.(),
    owns: node => !!node && (saveButton?.contains(node) || getFields().includes(node)),
    capture: () => ({ value: JSON.parse(snapshot(read())), values: values(getFields()), files: getFields().map(n => Array.from(n.files || [])), baseline: saving.has(h) && revision !== sentRevision ? sentSnapshot : record.baseline }),
    editedWhileSaving: () => saving.has(h) && revision !== sentRevision,
    restoreDraft(draft) {
      record.baseline = draft.baseline
      if (options.restore) options.restore(draft.value)
      if (!options.restore || options.restoreFields) getFields().forEach((n, i) => { if (n.type === 'file') { const transfer = new DataTransfer(); (draft.files[i] || []).forEach(f => transfer.items.add(f)); n.files = transfer.files; return }; if (n.type === 'radio' || n.type === 'checkbox') n.checked = draft.values[i]; else if (n.isContentEditable) n.textContent = draft.values[i]; else n.value = draft.values[i]; n.dispatchEvent(new Event('input', { bubbles: true })) })
      paint()
    },
    initial: () => JSON.parse(record.baseline),
    alive: () => (!ctx?.alive || ctx.alive()) && root.isConnected,
    dirty: () => dirty && changed(record.baseline, read()),
    status(text, bad = false) { model.message(text, bad); paint() },
    field: resolve,
    summary(items, prefix = 'Check: ') {
      root.querySelectorAll('[data-form-banner]').forEach(n => n.remove())
      const banner = document.createElement('p'); banner.className = 'note bad'; banner.dataset.formBanner = ''; banner.setAttribute('role', 'alert')
      function render() {
        const remaining = items.filter(item => model.errors.has(resolve(item.field)))
        banner.replaceChildren(document.createTextNode(prefix))
        remaining.forEach((item, i) => { if (i) banner.append(', '); banner.append(h.link(item.field, item.label)) })
        if (!remaining.length) banner.remove()
      }
      banner.update = render
      const head = root.querySelector('.ph')
      if (head) head.insertAdjacentElement('afterend', banner); else root.prepend(banner)
      render()
    },
    focus(field) { const n = resolve(field); if (!n) return; n.scrollIntoView({ block: 'center' }); n.focus({ preventScroll: true }) },
    link(field, text) { const a = document.createElement('button'); a.type = 'button'; a.className = 'field-link'; a.textContent = text; a.addEventListener('click', () => h.focus(field)); return a },
    fieldError(field, text) {
      const n = resolve(field)
      if (!n) { h.status(text, true); return }
      clearMark(n)
      const holder = n.closest('.field, fieldset') || n.parentElement
      const line = document.createElement('span'); line.className = 'err'; line.dataset.formError = ''; line.id = 'form-error-' + (++errorID); line.textContent = text
      const described = n.getAttribute('aria-describedby') || ''
      n.setAttribute('aria-invalid', 'true'); n.setAttribute('aria-describedby', (described + ' ' + line.id).trim()); holder.classList.add('has-error'); if (holder.matches('.check')) holder.after(line); else holder.append(line)
      marks.set(n, { line, holder, described }); model.error(n, text); paint()
    },
    error(text, field) {
      // Prefer an explicitly named control, then match the server's plain field wording.
      const n = resolve(field) || getFields().find(n => {
        const name = n.labels?.[0]?.textContent?.trim() || n.getAttribute('aria-label') || n.name || n.id
        return name && name.length > 2 && text.toLowerCase().includes(name.toLowerCase())
      })
      if (n) h.fieldError(n, text)
      h.status(text, true)
    },
    serverError(response, text) {
      const data = response.data || {}, code = data.code || ''
      const field = data.field || errorField(code)
      h.error(text || data.error || 'Not saved', field)
      if (Array.isArray(data.missing)) data.missing.forEach(f => h.fieldError(f, 'Add ' + f.replaceAll('_', ' ') + '.'))
    },
    clearErrors() {
      marks.forEach((_, n) => clearMark(n)); model.clear()
      root.querySelectorAll('.err:not([data-form-status]):not([data-form-error]), [id$=\"-err\"], [data-form-banner]').forEach(n => {
        const field = n.closest('.field')?.querySelector(controls)
        if (n.hasAttribute('data-form-banner')) n.remove()
        else if (!field || getFields().includes(field)) n.textContent = ''
      })
      getFields().forEach(n => n.removeAttribute('aria-invalid'))
      paint()
    },
    edited(field) { if (!restoring) revision++; if (field) { const n = resolve(field); for (const marked of [...marks.keys()]) if (marked === n || (n?.name && marked.name === n.name)) { clearMark(marked); model.edit(marked) }; n?.removeAttribute('aria-invalid'); model.edit(n) } else model.message(''); root.querySelectorAll('[data-form-banner]').forEach(n => { if (n.update) n.update(); else n.remove() }); paint() },
    saved(text = 'Saved', savedSnapshot) {
      // A callback can replace its own form before it returns. Finish on the
      // current registration, whose values include server normalization.
      const current = h.replacement || record.current
      if (current && current !== h) { current.saved(text, savedSnapshot); options.onSaved?.(text); return }
      h.clearErrors(); record.baseline = savedSnapshot ?? snapshot(read())
      initialFields = getFields(); initialValues = values(initialFields); model.saved(text); paint(); options.onSaved?.(text)
    },
    async discard() {
      if (discard) await discard(JSON.parse(record.baseline))
      else if (options.restore) options.restore(JSON.parse(record.baseline))
      else initialFields.forEach((n, i) => { if (n.type === 'checkbox' || n.type === 'radio') n.checked = initialValues[i]; else if (n.type === 'file') n.value = ''; else if (n.isContentEditable) n.textContent = initialValues[i]; else n.value = initialValues[i]; n.dispatchEvent(new Event('input', { bubbles: true })) })
      h.saved('')
    },
    save(event, action = save) {
      if (busy) return busy
      if (!action) return Promise.resolve(false)
      h.clearErrors()
      const invalid = getFields().filter(n => n.willValidate && !n.validity.valid)
      if (invalid.length) { invalid.forEach(n => h.fieldError(n, n.validationMessage)); h.focus(invalid[0]); return Promise.resolve(false) }
      sentRevision = revision; sentSnapshot = snapshot(read())
      saving.add(h)
      busy = (async () => {
        try {
          let pending
          const previous = initiating
          initiating = h
          try { pending = action(event || { preventDefault() {}, target: root, currentTarget: root }) }
          finally { initiating = previous }
          const result = await pending
          adoptErrors()
          if (result === false || result?.ok === false || (result !== true && model.bad)) { if (!model.bad) h.status('Not saved', true); const first = model.errors.keys().next().value; if (first) h.focus(first); return false }
          h.saved(model.status && !model.bad ? model.status : 'Saved', revision !== sentRevision ? sentSnapshot : undefined); return true
        } catch (e) { h.status(e.message === 'signed out' ? 'Not saved' : 'Cannot reach the server.', true); return false }
        finally { saving.delete(h); busy = null; paint() }
      })()
      return busy
    },
    canSave: !!save && options.saveOnLeave !== false,
    destroy() { observer?.disconnect(); root.removeEventListener('click', focusLink); root.removeEventListener('input', edit, true); root.removeEventListener('change', edit, true); root.removeEventListener('invalid', invalid, true); handles.delete(h) }
  }
  // Existing inline slots feed the same field/status lifecycle, including validation on change.
  let observer
  function adoptErrors() {
    root.querySelectorAll('.err:not([data-form-error]):not([data-form-status]), [id$="-err"]').forEach(n => {
      const text = n.textContent.trim()
      if (!text) return
      const field = n.closest('.field')?.querySelector(controls)
      if (field && !getFields().includes(field)) return
      if (!field && forElement(n) !== h) return
      n.textContent = ''
      h.error(text, field)
    })
  }
  function edit(e) {
    if (!getFields().includes(e.target)) return
    // Clear the field's older inline validation slot as well as its shared mark.
    e.target.closest('.field')?.querySelectorAll('.err:not([data-form-status]), [id$=\"-err\"]').forEach(n => { if (!n.hasAttribute('data-form-error')) n.textContent = '' })
    h.edited(e.target)
    queueMicrotask(() => { if (h.alive()) paint() })
  }
  function focusLink(e) { const link = e.target.closest('[data-field-focus]'); if (link) { e.preventDefault(); h.focus(link.dataset.fieldFocus) } }
  root.addEventListener('click', focusLink)
  function invalid(e) { e.preventDefault(); const first = model.errors.size === 0; h.fieldError(e.target, e.target.validationMessage); if (first) h.focus(e.target) }
  root.addEventListener('input', edit, true); root.addEventListener('change', edit, true); root.addEventListener('invalid', invalid, true)
  discardButton?.addEventListener('click', () => h.discard())
  record.current = h
  handles.add(h); paint(); bindOnce()
  observer = new MutationObserver(adoptErrors)
  observer.observe(root, { childList: true, characterData: true, subtree: true })
  return h
}
let errorID = 0
// Register a screen's existing async save callback and its button/native form together.
export function bind(ctx, target, save, options = {}) {
  if (!target) return null
  const form = target.matches('form') ? target : null
  if (form) form.noValidate = true
  const button = form ? form.querySelector('button:not([type=button]), [type=submit]') : target
  const root = options.root || form || target.closest('.card, .setting, .sp, main')
  const h = register(ctx, { root, save, saveButton: button, ...options })
  const submit = e => { e.preventDefault(); h.save(e) }
  if (form) onSubmit(form, button, submit)
  else target.addEventListener('click', submit)
  return h
}
// Shared close behavior for ui.dialog and the few sheets with a dynamic footer.
export function dialogForm({ ctx = currentContext, root, label, save, saveButton, snapshot: readSnapshot, saveOnLeave = true, cancelValue = null, saveValue = true, close }) {
  let finished = false, closing = false
  const finish = value => { if (finished) return; finished = true; h.destroy(); close(value) }
  const h = register(ctx, { root, label, save, saveButton, snapshot: readSnapshot, saveOnLeave,
    onSaved: text => { if (save && text) finish(saveValue) }, onExpire: () => finish(cancelValue) })
  return { handle: h, async done(value) {
    if (finished || closing) return
    closing = true
    try {
      if (value === cancelValue) { if (!await confirmLeave([h])) return }
      else if (save && !await h.save()) return
      h.saved(''); finish(value)
    } finally { closing = false }
  } }
}
let restoring = false
const preservations = new Set()
// Routers call this immediately before replacing the visible form, after reads.
export function checkpoint(ctx) {
  for (const job of preservations) if (job.ctx === ctx) job.capture()
}
// Local updates and refreshes carry drafts and the latest result to new handles.
export async function preserve(ctx, work) {
  if (restoring) return work()
  const job = { ctx, pending: [], capture() {
    this.pending = active().filter(h => h.ctx === ctx || h.ctx?.alive === ctx?.alive).map(h =>
      [h.key, { ...h.capture(), dirty: h.dirty() && (!saving.has(h) || h.editedWhileSaving()), handle: h }])
  } }
  job.capture(); preservations.add(job)
  try {
    const result = await work()
    restoring = true
    try {
      for (const [key, draft] of job.pending) {
        const h = active().find(h => h.key === key)
        if (h && h !== draft.handle) {
          draft.handle.replacement = h
          if (draft.dirty) h.restoreDraft(draft)
          if (draft.handle.model.status) h.status(draft.handle.model.status, draft.handle.model.bad)
        }
      }
    } finally { restoring = false }
    return result
  } finally { preservations.delete(job) }
}
export async function cancel(root, close) {
  const h = forElement(root)
  if (!h || await confirmLeave([h])) return close()
}
export function instant(ctx, root, options = {}) { return root && register(ctx, { root, dirty: false, ...options }) }
export function dirty(ctx) { return active().some(h => (!ctx || h.ctx === ctx) && !saving.has(h) && h.dirty()) }
export async function confirmLeave(only, saved) {
  const dirtyForms = () => (only || active()).filter(h => h.alive() && h !== saved && h.dirty()).sort((a, b) => Number(!!b.root.closest('.scrim')) - Number(!!a.root.closest('.scrim')))
  const pending = dirtyForms()
  if (!pending.some(h => h.dirty())) return true
  if (leaving) return leaving
  leaving = (async () => {
    const canSave = pending.every(h => h.canSave)
    const action = await dialog({ title: 'Unsaved changes', body: 'Save your changes before leaving?',
      buttons: [ { label: 'Stay', value: 'stay' }, { label: 'Discard', value: 'discard' }, ...(canSave ? [{ label: 'Save', value: 'save', className: 'btn primary' }] : []) ],
      escapeValue: 'stay', focus: 0, guard: false })
    if (action === 'stay') return false
    for (;;) {
      const h = dirtyForms()[0]
      if (!h) return true
      if (action === 'discard') await h.discard()
      else if (!await h.save()) return false
    }
  })()
  try { return await leaving } finally { leaving = null }
}
function bindOnce() {
  if (bound) return
  bound = true
  window.addEventListener('beforeunload', e => { if (active().some(h => h.dirty())) { e.preventDefault(); e.returnValue = '' } })
}
export function dialogContext() { return currentContext }
