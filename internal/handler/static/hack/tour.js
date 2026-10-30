import { tours } from './tours.js'

// Guided, non-modal walkthroughs (atlas: docs/ux-atlas/spec.json "walkthroughs").
// A small card sits next to the step's real target. No dark overlay: the
// page underneath stays clickable and the person can navigate away at any
// time, which simply tears this down. One tour runs at a time.
//
// startTour(role, ctx, { force }) — ctx is the screen's render ctx (needs
// .api, .ui, .me, .alive()). Without force it checks the stored seen-state
// itself (the server preference for a `pref` tour, localStorage for a
// `localKey` one -- see tourDone/markTourDone) and does nothing if that
// tour is already marked done; with force it always shows (the "Replay
// walkthrough" link, and the shell Help menu's ?walkthrough=1).

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
  // A22: a nav-link step (".nav a[...]") always opens the phone menu sheet
  // and points inside it, even when the step also carries a phone-specific
  // fallback -- one way to reach a section's nav link on phone, not two.
  const sel = (isPhone() && step.phone && !/^\.nav /.test(step.target || '')) ? step.phone : step.target
  if (!sel) return null
  let el
  try { el = document.querySelector(sel) } catch (e) { return null }
  return visible(el) ? el : null
}
// C4: a step whose target isn't on the screen yet (e.g. the judge tour
// before scoring opens, when Home shows only "Not open yet") is skipped
// rather than shown pointing at nothing -- a step with no target at all is
// that way by design (a centred welcome/explainer card) and always shows.
// A step can also carry `when(ctx)`: a stage check for a step whose target
// is always in the DOM (e.g. the shell's own `.status` bar) but whose copy
// only makes sense in some stages -- the judge Home tour uses this so the
// "is scoring open?" step disappears once judging is locked or results are
// out, instead of a target-presence check that would never catch it.
function canShow(step, ctx) {
  if (step.when && !step.when(ctx)) return false
  if (!step.target) return true
  const app = document.querySelector('.app')
  const navStep = isPhone() && /^\.nav /.test(step.target || '')
  if (app) app.classList.toggle('menu-open', navStep)
  return !!resolveTarget(step)
}
// Walks from `from` in direction `dir` (1 or -1) to the nearest step that
// can actually show, or -1 if none can.
function nextShowable(spec, from, dir, ctx) {
  let i = from
  while (i >= 0 && i < spec.steps.length) {
    if (canShow(spec.steps[i], ctx)) return i
    i += dir
  }
  return -1
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
  document.querySelector('.app')?.classList.remove('menu-open')
  a.card.remove()
  try { if (a.previous && a.previous.focus) a.previous.focus() } catch (e) {}
}

// Seen-state lives in one of two places. `spec.pref` tours (organiser,
// judge Home) use the server preference -- it survives a new device. Every
// other tour has no server field for it (the preferences API only has
// organiser_walkthrough_done and judge_walkthrough_done), so `spec.localKey`
// tours use localStorage instead, keyed per signed-in person so one shared
// browser doesn't mark the tour seen for an account that never saw it.
function localSeenKey(spec, ctx) {
  const uid = (ctx.me && (ctx.me.id || ctx.me.username)) || 'anon'
  return spec.localKey + '-' + uid
}
async function tourDone(ctx, spec) {
  if (spec.pref) {
    let prefs
    try { prefs = await ctx.api.api('GET', '/v1/hack/preferences') } catch (e) { prefs = { ok: false } }
    if (ctx.alive && !ctx.alive()) return true // superseded navigation: treat as "don't show"
    return !!(prefs.ok && prefs.data && prefs.data[spec.pref])
  }
  if (spec.localKey) {
    try { return localStorage.getItem(localSeenKey(spec, ctx)) === '1' } catch (e) { return false }
  }
  return false
}
function markTourDone(ctx, spec) {
  if (spec.pref) { ctx.api.api('PATCH', '/v1/hack/preferences', { [spec.pref]: true }).catch(() => {}); return }
  if (spec.localKey) { try { localStorage.setItem(localSeenKey(spec, ctx), '1') } catch (e) {} }
}

export async function startTour(role, ctx, { force = false } = {}) {
  const spec = tours[role]
  if (!spec) return
  if (!force) {
    const done = await tourDone(ctx, spec)
    if (ctx.alive && !ctx.alive()) return
    if (done) return
  }
  if (ctx.alive && !ctx.alive()) return

  // S9: a tour opened via Help's ?walkthrough=1 link leaves that param
  // sitting in the URL, which would reopen the tour on a reload.
  const url = new URL(location.href)
  if (url.searchParams.has('walkthrough')) {
    url.searchParams.delete('walkthrough')
    const q = url.searchParams.toString()
    history.replaceState({}, '', url.pathname + (q ? '?' + q : '') + url.hash)
  }

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
    markTourDone(ctx, spec)
  }

  function reposition() {
    if (active !== state) return
    place(card, state.target)
  }

  function show(i) {
    state.index = Math.min(Math.max(i, 0), spec.steps.length - 1)
    const step = spec.steps[state.index]
    if (state.target) state.target.classList.remove('tour-ring')
    // Checked before touching .menu-open below -- nextShowable's own lookahead
    // (and the shownCount scan right after it) toggles that class on earlier
    // steps as they scan, so every side-effecting lookahead has to run
    // first, and the real toggle for this step last.
    const backDisabled = nextShowable(spec, state.index - 1, -1, ctx) === -1
    const isLastShown = nextShowable(spec, state.index + 1, 1, ctx) === -1
    // Review #17: "Step N of M" and the Back/Next labels must count only
    // the steps that will actually show (a skipped step, e.g. the judge
    // scoring step before scoring opens, shouldn't inflate M or leave N
    // stuck below it), and Next must read "Done" on the last step that
    // actually shows, not the last one in the spec.
    let shown = 0, shownTotal = 0
    for (let j = 0; j < spec.steps.length; j++) {
      if (canShow(spec.steps[j], ctx)) { shownTotal++; if (j <= state.index) shown++ }
    }
    // A22: a nav-link step opens the phone menu sheet so its target is
    // actually on screen to point at, instead of ringing the "Menu" button
    // itself on every section step in a row.
    const app = document.querySelector('.app')
    const navStep = isPhone() && /^\.nav /.test(step.target || '')
    if (app) app.classList.toggle('menu-open', navStep)
    const target = resolveTarget(step)
    state.target = target
    card.querySelector('.tour-progress').textContent = `Step ${shown} of ${shownTotal}`
    card.querySelector('.tour-title').textContent = step.title
    card.querySelector('.tour-body').textContent = step.body
    card.querySelector('[data-tour-back]').disabled = backDisabled
    card.querySelector('[data-tour-next]').textContent = isLastShown ? 'Done' : 'Next'
    if (target) {
      target.classList.add('tour-ring')
      try { target.scrollIntoView({ behavior: 'smooth', block: 'nearest' }) } catch (e) {}
    }
    place(card, target)
    requestAnimationFrame(() => place(card, target))
    card.focus()
  }

  function back() { const i = nextShowable(spec, state.index - 1, -1, ctx); if (i !== -1) show(i) }
  function next() { const i = nextShowable(spec, state.index + 1, 1, ctx); if (i === -1) finish(); else show(i) }

  card.querySelector('.tour-skip').addEventListener('click', finish)
  card.querySelector('[data-tour-back]').addEventListener('click', back)
  card.querySelector('[data-tour-next]').addEventListener('click', next)

  function onKey(e) {
    if (active !== state) return
    // Review #3: the tour is non-modal by design, so the person can type
    // with the card open -- a key typed into a real field is text, not a
    // tour command, even Escape (which would otherwise dismiss the tour
    // instead of, say, blurring a field).
    if (e.target && e.target.closest && e.target.closest('input,textarea,select,[contenteditable],[role=radio]')) return
    // A confirm dialog owns the keyboard while it is open (Esc cancels it, not the tour).
    if (document.querySelector('.scrim')) return
    if (e.key === 'Escape') { e.preventDefault(); finish() }
    else if (e.key === 'ArrowRight') { e.preventDefault(); next() }
    else if (e.key === 'ArrowLeft') { e.preventDefault(); back() }
  }
  // The page stays clickable, including its own navigation. A click on a
  // link elsewhere means the person is moving to another screen: let the
  // app's own click handler do the navigating and just clean up this tour
  // so a stale card or highlight never survives the route change. Review
  // #17: following the ringed link itself is the person acting on the step
  // just shown them, so that one click also marks the tour seen, instead of
  // leaving it to re-pop on their next visit.
  function onClick(e) {
    if (e.target.closest('.tour-card')) return
    const link = e.target.closest('a[href]')
    if (!link) return
    if (state.target && (state.target === link || state.target.contains(link))) finish()
    else teardown()
  }
  document.addEventListener('keydown', onKey, true)
  document.addEventListener('click', onClick, true)
  window.addEventListener('resize', reposition)
  window.addEventListener('scroll', reposition, true)
  state.onKey = onKey
  state.onClick = onClick
  state.reposition = reposition

  // Review #7: when nothing in this tour can actually show (e.g. a judge on
  // a locked scoring page with no site, or a replay forced onto a step with
  // no live target), there is no reasonable step to default to -- showing
  // step 0 anyway puts a centred card up pointing at nothing. Just don't
  // start.
  const start = nextShowable(spec, 0, 1, ctx)
  if (start === -1) { teardown(); return }
  show(start)
}

// Review #2: a route change (any navigation, including a button that calls
// ctx.go() rather than a plain link) must clear a running tour -- the card
// is appended to document.body, so remounting #app alone doesn't remove it,
// and it would otherwise outlive the screen it was pointing at. Exported so
// app.js can call it at the top of every route change.
export function endTour() { teardown() }
