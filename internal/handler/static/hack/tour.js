import { tours } from './tours.js'

// Guided, non-modal walkthroughs (atlas: docs/ux-atlas/spec.json "walkthroughs").
// A small card sits next to the step's real target. No dark overlay: the
// page underneath stays clickable and the person can navigate away at any
// time, which simply tears this down. One tour runs at a time.
//
// startTour(role, ctx, { force }) — ctx is the screen's render ctx (needs
// .api, .ui, .alive()). Without force it checks the stored preference
// itself and does nothing if the role's tour is already marked done; with
// force it always shows (the "Replay walkthrough" link, and the shell
// Help menu's ?walkthrough=1).

let active = null

function isPhone() {
  return window.innerWidth <= 760
}

function visible(el) {
  if (!el || !el.isConnected) return false
  const style = getComputedStyle(el)
  if (style.display === 'none' || style.visibility === 'hidden') return false
  const r = el.getBoundingClientRect()
  return r.width > 0 && r.height > 0
}

function resolveTarget(step) {
  const sel = (isPhone() && step.phone) ? step.phone : step.target
  if (!sel) return null
  let el
  try { el = document.querySelector(sel) } catch (e) { return null }
  return visible(el) ? el : null
}

function contentBox() {
  const el = document.querySelector('.content') || document.body
  return el.getBoundingClientRect()
}

// Keeps the card inside the viewport: below the target if it fits, else
// above, else clamped to the viewport; always clamped horizontally.
function place(card, target) {
  const pad = 8, gap = 12
  const vw = document.documentElement.clientWidth
  const vh = document.documentElement.clientHeight
  const c = card.getBoundingClientRect()
  let top, left
  if (target) {
    const t = target.getBoundingClientRect()
    if (t.bottom + gap + c.height <= vh) top = t.bottom + gap
    else if (t.top - gap - c.height >= 0) top = t.top - gap - c.height
    else top = t.top
    left = t.left + t.width / 2 - c.width / 2
  } else {
    const box = contentBox()
    top = box.top + 20
    left = box.left + (box.width - c.width) / 2
  }
  left = Math.min(Math.max(left, pad), Math.max(pad, vw - c.width - pad))
  top = Math.min(Math.max(top, pad), Math.max(pad, vh - c.height - pad))
  card.style.left = left + 'px'
  card.style.top = top + 'px'
}

function teardown() {
  if (!active) return
  const a = active
  active = null
  document.removeEventListener('keydown', a.onKey, true)
  document.removeEventListener('click', a.onClick, true)
  window.removeEventListener('resize', a.reposition)
  window.removeEventListener('scroll', a.reposition, true)
  if (a.target) a.target.classList.remove('tour-ring')
  a.card.remove()
  try { if (a.previous && a.previous.focus) a.previous.focus() } catch (e) {}
}

async function savePref(ctx, role) {
  try { await ctx.api.api('PATCH', '/v1/hack/preferences', { [tours[role].pref]: true }) } catch (e) {}
}

export async function startTour(role, ctx, { force = false } = {}) {
  const spec = tours[role]
  if (!spec) return
  if (!force) {
    let prefs
    try { prefs = await ctx.api.api('GET', '/v1/hack/preferences') } catch (e) { prefs = { ok: false } }
    if (ctx.alive && !ctx.alive()) return
    if (prefs.ok && prefs.data && prefs.data[spec.pref]) return
  }
  if (ctx.alive && !ctx.alive()) return

  teardown() // only one tour at a time

  const previous = document.activeElement
  const card = document.createElement('div')
  card.className = 'tour-card'
  card.setAttribute('role', 'dialog')
  card.setAttribute('aria-modal', 'false')
  card.setAttribute('aria-labelledby', 'tour-title')
  card.tabIndex = -1
  card.innerHTML = `<p class="tour-progress"></p><h2 class="tour-title" id="tour-title"></h2><p class="tour-body"></p><div class="tour-actions"><button type="button" class="btn quiet sm tour-skip">Skip</button><div class="tour-nav"><button type="button" class="btn sm" data-tour-back>Back</button><button type="button" class="btn primary sm" data-tour-next>Next</button></div></div>`
  document.body.append(card)

  const state = { card, previous, index: 0, target: null }
  active = state

  function finish() {
    teardown()
    savePref(ctx, role)
  }

  function reposition() {
    if (active !== state) return
    place(card, state.target)
  }

  function show(i) {
    state.index = Math.min(Math.max(i, 0), spec.steps.length - 1)
    const step = spec.steps[state.index]
    if (state.target) state.target.classList.remove('tour-ring')
    const target = resolveTarget(step)
    state.target = target
    card.querySelector('.tour-progress').textContent = `Step ${state.index + 1} of ${spec.steps.length}`
    card.querySelector('.tour-title').textContent = step.title
    card.querySelector('.tour-body').textContent = step.body
    card.querySelector('[data-tour-back]').disabled = state.index === 0
    card.querySelector('[data-tour-next]').textContent = state.index === spec.steps.length - 1 ? 'Done' : 'Next'
    if (target) {
      target.classList.add('tour-ring')
      try { target.scrollIntoView({ behavior: 'smooth', block: 'nearest' }) } catch (e) {}
    }
    place(card, target)
    requestAnimationFrame(() => place(card, target))
    card.focus()
  }

  function back() { if (state.index > 0) show(state.index - 1) }
  function next() { if (state.index === spec.steps.length - 1) finish(); else show(state.index + 1) }

  card.querySelector('.tour-skip').addEventListener('click', finish)
  card.querySelector('[data-tour-back]').addEventListener('click', back)
  card.querySelector('[data-tour-next]').addEventListener('click', next)

  function onKey(e) {
    if (active !== state) return
    if (e.key === 'Escape') { e.preventDefault(); finish() }
    else if (e.key === 'ArrowRight') { e.preventDefault(); next() }
    else if (e.key === 'ArrowLeft') { e.preventDefault(); back() }
  }
  // The page stays clickable, including its own navigation. A click on a
  // link elsewhere means the person is moving to another screen: let the
  // app's own click handler do the navigating and just clean up this tour
  // so a stale card or highlight never survives the route change.
  function onClick(e) {
    if (e.target.closest('.tour-card')) return
    if (e.target.closest('a[href]')) teardown()
  }
  document.addEventListener('keydown', onKey, true)
  document.addEventListener('click', onClick, true)
  window.addEventListener('resize', reposition)
  window.addEventListener('scroll', reposition, true)
  state.onKey = onKey
  state.onClick = onClick
  state.reposition = reposition

  show(0)
}
