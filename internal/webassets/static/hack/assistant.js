// SIM-394: organiser assistant chat. Built exactly to the fixed API contract
// (GET/POST /v1/hack/events/{slug}/assistant) -- see the ticket's
// api-contract.md. This module owns the whole feature: the Ask toggle, the
// panel (desktop side panel / phone sheet), the safe markdown renderer, the
// SSE stream reader and the per-browser history in localStorage. Screens
// that want the assistant call mountAssistant() once per render; it does
// nothing (no button at all) when the server says the assistant isn't
// configured.
//
// Never changes anything: there is no write call anywhere in this file.
//
// Review fixes (round 2): the DOM panel is ephemeral -- a screen re-render
// (Overview refresh on window focus, a stage change, leaving and coming
// back) tears it down and mountAssistant runs again from scratch. A chat in
// progress must not care: the conversation itself (messages, the in-flight
// reply, the AbortController, whether the panel was left open, the
// remaining-today count) now lives in a module-level map keyed by the same
// string as localStorage, so it survives any number of mounts. Each mount
// just re-attaches a fresh view to whatever that state currently holds.

import { el, copyButton } from './ui.js'
import { hasAssistant } from './platform.js'

const FOCUSABLE_SEL = 'a[href],button,input,select,textarea,[tabindex]'
const MAX_MESSAGES = 20
const MAX_TOTAL_CHARS = 12000
const MAX_MSG_CHARS = 2000
const MOUNT_RACE_MS = 1500

function isPhone() { return window.innerWidth <= 760 }

// M4: code points, not UTF-16 units -- a reply full of emoji or non-Latin
// text must not look "too long" (or "not too long") by the wrong yardstick.
function cpLen(s) { return [...String(s || '')].length }

// M7: the history (and the in-memory chat state below) is keyed by the
// signed-in user, not just the event -- two people sharing a browser must
// never see each other's questions. ctx.me is the same object tour.js and
// participant-home.js already key off (ctx.me.id, falling back to
// ctx.me.username). No usable id at all (should not happen once signed in,
// but never say never) -- fall back to slug-only so the feature still
// works; it just can't be told apart from another signed-out-ish session,
// which sign-out already wipes anyway.
function storageKey(slug, userId) {
  return (userId !== null && userId !== undefined && userId !== '') ? 'sh-assistant-' + userId + '-' + slug : 'sh-assistant-' + slug
}

function loadHistory(key) {
  try {
    const raw = localStorage.getItem(key)
    if (!raw) return []
    const parsed = JSON.parse(raw)
    if (!parsed || !Array.isArray(parsed.messages)) return []
    return parsed.messages.filter(m => m && (m.role === 'user' || m.role === 'assistant') && typeof m.content === 'string')
  } catch (e) { return [] }
}
function saveHistory(key, messages) {
  try { localStorage.setItem(key, JSON.stringify({ messages, updated: Date.now() })) } catch (e) {}
}
function clearHistory(key) {
  try { localStorage.removeItem(key) } catch (e) {}
}
// M7: every sign-out (the explicit button in api.js, and the 401 ->
// signedOut() path in app.js) clears every chat this browser ever held for
// anyone, not just the current event -- a shared computer must not leak
// the previous person's questions into the next sign-in.
export function clearAllAssistantHistory() {
  try {
    const stale = []
    for (let i = 0; i < localStorage.length; i++) {
      const k = localStorage.key(i)
      if (k && k.indexOf('sh-assistant-') === 0) stale.push(k)
    }
    stale.forEach(k => { try { localStorage.removeItem(k) } catch (e) {} })
  } catch (e) {}
  // Stop any reply still streaming so it can't write the old chat back.
  chatStates.forEach(st => { st.turnId++; try { st.abortController?.abort() } catch (e) {} })
  chatStates.clear()
}

// Trim oldest-first so a send never exceeds the API's own limits (1..20
// messages, total <= 12000 code points) -- applied every time the list
// changes, not just once, since the running total only grows as replies
// come in. L1: once trimmed, the list must still start with a user turn --
// a leading assistant message with its question cut off is just confusing.
function capMessages(messages) {
  const list = messages.slice()
  while (list.length > MAX_MESSAGES) list.shift()
  let total = list.reduce((n, m) => n + cpLen(m.content), 0)
  while (total > MAX_TOTAL_CHARS && list.length > 0) { total -= cpLen(list[0].content); list.shift() }
  while (list.length && list[0].role === 'assistant') list.shift()
  return list
}

// ---- safe markdown renderer --------------------------------------------
// Never innerHTML of model text. Paragraphs, line breaks, **bold**,
// *italic*, `inline code`, fenced code blocks, - / 1. lists, [label](href)
// links. A link renders as <a> only when its href is relative and starts
// with /e/{slug}/ or /docs/; anything else renders as its plain label.
// Models sometimes put a space, an invisible character or a second slash
// in an href; read those as the app path (the eval grader does the same).
function normaliseHref(href) {
  const h = String(href || '').replace(/[\s​-‏⁠﻿]/g, '')
  return /^\/\/(e|docs)\//.test(h) ? h.slice(1) : h
}
function allowedHref(h, slug) {
  return (h.startsWith('/e/' + slug + '/') || h.startsWith('/docs/')) && !h.includes('..')
}
const INLINE_RE = /`([^`]+)`|\[([^\]]+)\]\(([^)]+)\)|\*\*([^*]+)\*\*|\*([^*]+)\*/g
function appendInline(node, text, slug) {
  let last = 0, m
  INLINE_RE.lastIndex = 0
  while ((m = INLINE_RE.exec(text))) {
    if (m.index > last) node.append(document.createTextNode(text.slice(last, m.index)))
    if (m[1] !== undefined) { node.append(el('code', { text: m[1] })) }
    else if (m[2] !== undefined) {
      const href = normaliseHref(m[3])
      if (allowedHref(href, slug)) node.append(el('a', { href, text: m[2] }))
      else node.append(document.createTextNode(m[2]))
    } else if (m[4] !== undefined) node.append(el('strong', { text: m[4] }))
    else if (m[5] !== undefined) node.append(el('em', { text: m[5] }))
    last = INLINE_RE.lastIndex
  }
  if (last < text.length) node.append(document.createTextNode(text.slice(last)))
}
function renderMarkdownInto(container, text, slug) {
  const lines = String(text || '').split('\n')
  let i = 0
  while (i < lines.length) {
    const line = lines[i]
    if (/^```/.test(line)) {
      i++
      const codeLines = []
      while (i < lines.length && !/^```/.test(lines[i])) { codeLines.push(lines[i]); i++ }
      i++
      const pre = document.createElement('pre')
      pre.append(el('code', { text: codeLines.join('\n') }))
      container.append(pre)
      continue
    }
    if (line.trim() === '') { i++; continue }
    if (/^[-*]\s+/.test(line)) {
      const ul = document.createElement('ul')
      while (i < lines.length && /^[-*]\s+/.test(lines[i])) {
        const li = document.createElement('li')
        appendInline(li, lines[i].replace(/^[-*]\s+/, ''), slug)
        ul.append(li); i++
      }
      container.append(ul)
      continue
    }
    if (/^\d+\.\s+/.test(line)) {
      const ol = document.createElement('ol')
      while (i < lines.length && /^\d+\.\s+/.test(lines[i])) {
        const li = document.createElement('li')
        appendInline(li, lines[i].replace(/^\d+\.\s+/, ''), slug)
        ol.append(li); i++
      }
      container.append(ol)
      continue
    }
    const paraLines = []
    while (i < lines.length && lines[i].trim() !== '' && !/^```/.test(lines[i]) && !/^[-*]\s+/.test(lines[i]) && !/^\d+\.\s+/.test(lines[i])) {
      paraLines.push(lines[i]); i++
    }
    const p = document.createElement('p')
    paraLines.forEach((l, idx) => { if (idx > 0) p.append(document.createElement('br')); appendInline(p, l, slug) })
    container.append(p)
  }
}

// ---- SSE reader ----------------------------------------------------------
// `data: {json}\n\n` lines, per the contract. Calls onEvent for each parsed
// JSON object, in order.
async function readSSE(response, onEvent) {
  const reader = response.body.getReader()
  const decoder = new TextDecoder()
  let buf = ''
  while (true) {
    const { done, value } = await reader.read()
    if (done) break
    buf += decoder.decode(value, { stream: true })
    let idx
    while ((idx = buf.indexOf('\n\n')) !== -1) {
      const chunk = buf.slice(0, idx)
      buf = buf.slice(idx + 2)
      const line = chunk.split('\n').find(l => l.startsWith('data:'))
      if (!line) continue
      const jsonStr = line.slice(5).trim()
      if (!jsonStr) continue
      let data
      try { data = JSON.parse(jsonStr) } catch (e) { continue }
      onEvent(data)
    }
  }
}

// ---- starter chips (point 9) ---------------------------------------------
function stageChip(stage) {
  if (stage === 'draft' || stage === 'open') return 'Is my setup done?'
  if (stage === 'building') return 'What happens when I close entries?'
  if (stage === 'closed' || stage === 'judging') return 'How do I lock judging?'
  if (stage === 'results') return 'How do I publish results?'
  if (stage === 'archived' || stage === 'ended') return 'Where do I download the results?'
  return 'Is my setup done?'
}
// checklist.next.label, else the first not-done item's label; "What does
// "<label>" mean?" when one exists, else the fixed fallback question.
function checklistChipSafe(checklist) {
  const items = (checklist && checklist.items) || []
  const label = (checklist && checklist.next && checklist.next.label) || (items.find(it => it.state !== 'done') || {}).label
  return label ? ('What does "' + label + '" mean?') : 'What should I do next?'
}
function starterChips(stage, checklist) {
  const raw = [stageChip(stage), checklistChipSafe(checklist), 'Where do I find the join and judge links?']
  const seen = new Set()
  const out = []
  for (const c of raw) { if (c && !seen.has(c)) { seen.add(c); out.push(c) } }
  return out
}

const MONTH_NAMES = ['January', 'February', 'March', 'April', 'May', 'June', 'July', 'August', 'September', 'October', 'November', 'December']
// "2026-12-01" -> "1 December" (L2's exact wording for the monthly line).
function formatResetDate(iso) {
  const m = /^(\d{4})-(\d{2})-(\d{2})/.exec(String(iso || ''))
  if (!m) return String(iso || '')
  const month = MONTH_NAMES[parseInt(m[2], 10) - 1]
  return month ? (parseInt(m[3], 10) + ' ' + month) : String(iso)
}
function monthlyBudgetLine(resets) { return "The assistant has used this month's budget. It's back on " + formatResetDate(resets) + '.' }
function dailyLimitLine(dailyLimit) { return "You've asked " + dailyLimit + ' questions today. The limit resets at midnight UTC.' }

// ---- module-level chat state (H2) -----------------------------------------
// One entry per storage key, created lazily, never torn down by a mount --
// only New chat (and sign-out, via clearAllAssistantHistory) clears it.
// A run of runTurn keeps writing into this object (messages, turnAcc,
// remainingToday, streaming, abortController) regardless of whether any
// panel is currently attached to the page; a view just reflects whatever
// is in here right now, and re-reflects it from scratch every time it
// (re)mounts.
const chatStates = new Map()
function getChatState(key) {
  let s = chatStates.get(key)
  if (!s) {
    s = {
      key,
      loaded: false,
      messages: [],
      remainingToday: null,
      limitMessage: '',
      streaming: false,
      turnId: 0,
      turnAcc: '',
      abortController: null,
      open: false,
      // The DOM panel currently showing this chat, shared across mounts
      // (see the H2 note above the map) so an in-flight stream started by
      // an older, now-torn-down mount keeps updating whichever panel is
      // actually on screen right now, not a detached one nobody can see.
      panelEl: null
    }
    chatStates.set(key, s)
  }
  return s
}

// ---- the module -----------------------------------------------------------
// mountAssistant(ctx, { api, anchor, container, ev, checklist }):
// - `ctx` is the screen's render ctx (api/ui/me/alive()).
// - `api` is the plain api(method, path) function (ctx.api.api).
// - `anchor` is the element the Ask button is inserted right after.
// - `container` is the screen's own <main> -- the panel is appended inside
//   it (position:fixed still anchors to the viewport; see kit.css), so its
//   DOM is torn down for free the next time this screen re-renders its
//   innerHTML. The conversation itself does not depend on that DOM; see the
//   module state above.
// - `ev` is the event object (slug, stage).
// - `checklist` is the organiser's own checklist object, or undefined.
//
// L11: the GET this makes can be slow (or never configured, in which case
// it still has to try once). A caller that awaits this (organiser-
// overview.js, so the tour's target exists before the tour starts
// scanning the DOM) must not be stuck behind it -- raced against a fixed
// timeout; the mount itself keeps going in the background and still
// appears whenever the GET actually lands, just possibly after the tour
// already ran (tour.js already tolerates a step target that isn't there).
export async function mountAssistant(ctx, opts) {
  const mounted = doMount(ctx, opts)
  await Promise.race([mounted, new Promise(resolve => setTimeout(resolve, MOUNT_RACE_MS))])
}

async function doMount(ctx, { api, anchor, container, ev, checklist }) {
  if (!hasAssistant()) return
  const slug = ev.slug
  let status
  try { status = await api('GET', '/v1/hack/events/' + encodeURIComponent(slug) + '/assistant') }
  catch (e) { return }
  if (ctx.alive && !ctx.alive()) return
  if (!status.ok || !status.data || status.data.enabled !== true) return
  if (!anchor || !anchor.isConnected) return

  const userId = (ctx.me && (ctx.me.id ?? ctx.me.username)) ?? null
  const key = storageKey(slug, userId)
  const state = getChatState(key)
  if (!state.loaded) {
    state.loaded = true
    state.messages = capMessages(loadHistory(key))
    state.remainingToday = typeof status.data.remaining_today === 'number' ? status.data.remaining_today : null
  }
  // L2: the GET's own budget/limit fields, applied once (never cleared here
  // -- only a fresh GET on a later mount could say otherwise, and this
  // server-side fact doesn't flip back on its own within a page's life).
  if (!state.limitMessage) {
    if (status.data.budget_reached) state.limitMessage = monthlyBudgetLine(status.data.resets)
    else if (status.data.remaining_today === 0) state.limitMessage = dailyLimitLine(status.data.daily_limit)
  }

  const askBtn = el('button', { class: 'btn quiet sm', type: 'button', 'data-tour': 'assistant', text: 'Ask', 'aria-expanded': 'false', 'aria-controls': 'assistant-panel-' + slug })
  anchor.insertAdjacentElement('afterend', askBtn)


  function apiKeyHeader() { try { return ctx.api.apiKey() } catch (e) { return '' } }

  function scrollToBottom(thread) { if (thread) thread.scrollTop = thread.scrollHeight }

  function renderHint() {
    if (!state.panelEl) return
    const hintEl = state.panelEl.querySelector('[data-assistant-hint]')
    if (!hintEl) return
    if (state.limitMessage) { hintEl.textContent = state.limitMessage; hintEl.className = 'note bad small assistant-hint'; hintEl.hidden = false; return }
    if (typeof state.remainingToday === 'number' && state.remainingToday <= 5) {
      const n = state.remainingToday
      hintEl.textContent = n + (n === 1 ? ' question left today.' : ' questions left today.')
      hintEl.className = 'small muted assistant-hint'
      hintEl.hidden = false
      return
    }
    hintEl.hidden = true
  }

  function setRemaining(n) {
    if (typeof n === 'number') state.remainingToday = n
    renderHint()
  }
  // L12: an aborted or failed stream (no `done` event, so no server figure
  // to trust) still spent a question server-side -- approximate it locally
  // so the hint doesn't lie by one until the next GET.
  function bumpRemainingLocal() {
    if (typeof state.remainingToday === 'number') state.remainingToday = Math.max(0, state.remainingToday - 1)
    renderHint()
  }

  function disableForLimit(message) {
    state.limitMessage = message
    if (!state.panelEl) return
    const ta = state.panelEl.querySelector('[data-assistant-input]')
    const send = state.panelEl.querySelector('[data-assistant-send]')
    if (ta) ta.disabled = true
    if (send) send.disabled = true
    renderHint()
    refocusAfterTurn()
  }

  function announce(text) {
    const live = state.panelEl && state.panelEl.querySelector('[data-assistant-live]')
    if (!live) return
    live.textContent = ''
    requestAnimationFrame(() => { if (live.isConnected) live.textContent = text })
  }

  // L6: once a turn ends (or the input just got disabled for a limit),
  // focus should never be left stranded on the Send button or the body --
  // move it to the textarea, or to Close when the textarea is now disabled.
  function refocusAfterTurn() {
    if (!state.panelEl) return
    const active = document.activeElement
    if (active !== (state.panelEl.querySelector('[data-assistant-send]')) && active !== document.body) return
    const ta = state.panelEl.querySelector('[data-assistant-input]')
    const close = state.panelEl.querySelector('[data-assistant-close]')
    if (ta && !ta.disabled) ta.focus()
    else if (close) close.focus()
  }

  function syncSendButton() {
    if (!state.panelEl) return
    const ta = state.panelEl.querySelector('[data-assistant-input]')
    const send = state.panelEl.querySelector('[data-assistant-send]')
    const thread = state.panelEl.querySelector('[data-assistant-thread]')
    if (thread) thread.setAttribute('aria-busy', state.streaming ? 'true' : 'false')
    if (!ta || !send) return
    if (state.streaming) { send.textContent = 'Stop'; send.disabled = false; return }
    send.textContent = 'Send'
    send.disabled = state.limitMessage ? true : ta.value.trim() === ''
  }

  function renderAssistantBubble(thread, text, { thinking, acts } = {}) {
    const bubble = document.createElement('div')
    bubble.className = 'assistant-bubble'
    if (thinking) bubble.append(el('p', { class: 'assistant-thinking', text: 'Thinking…' }))
    else renderMarkdownInto(bubble, text, slug)
    const wrap = document.createElement('div')
    wrap.className = 'assistant-msg assistant'
    wrap.append(bubble)
    if (acts) wrap.append(acts)
    thread.append(wrap)
    return wrap
  }
  function retryActsRow(onRetry) {
    const acts = document.createElement('div')
    acts.className = 'assistant-acts'
    const retryBtn = el('button', { class: 'btn quiet sm', type: 'button', 'data-assistant-retry': '', text: 'Retry' })
    retryBtn.addEventListener('click', onRetry)
    acts.append(retryBtn)
    return acts
  }
  function appendErrorLine(thread, message, { retry = true } = {}) {
    if (!thread) return
    const row = document.createElement('div')
    row.className = 'note bad small assistant-error'
    row.append(el('span', { text: message }))
    // L5: no point offering Retry once the account itself is capped for the
    // day/month -- retrying just fails the same way again.
    if (retry) {
      const retryBtn = el('button', { class: 'btn quiet sm', type: 'button', text: 'Retry' })
      retryBtn.addEventListener('click', () => { row.remove(); retryLast() })
      row.append(retryBtn)
    }
    thread.append(row)
    scrollToBottom(thread)
  }

  function hideChipsAndSub() {
    if (!state.panelEl) return
    const chips = state.panelEl.querySelector('[data-assistant-chips]')
    const sub = state.panelEl.querySelector('[data-assistant-sub]')
    if (chips) chips.remove()
    if (sub) sub.remove()
  }

  // Rebuild of the thread from state -- messages, the in-flight turn (if
  // any), and a Retry affordance when history ends on an unanswered user
  // turn (L4). Cheap enough at this scale (<=20 messages) and the one
  // thing that is guaranteed correct on every remount, instead of trying to
  // patch stale DOM nodes that may belong to a torn-down mount.
  //
  // Only removes nodes it marked data-assistant-structural itself -- a
  // transient error line (appendErrorLine) is not state-driven and must
  // survive a later turn's own render; each failed attempt's error line
  // accumulates rather than getting wiped by the next send.
  function renderThread() {
    if (!state.panelEl) return
    const thread = state.panelEl.querySelector('[data-assistant-thread]')
    if (!thread) return
    thread.querySelectorAll('[data-assistant-structural]').forEach(n => n.remove())
    const frag = document.createDocumentFragment()
    state.messages.forEach((m, idx) => {
      const isLast = idx === state.messages.length - 1
      if (m.role === 'user') {
        const node = el('div', { class: 'assistant-msg user', text: m.content })
        node.setAttribute('data-assistant-structural', '')
        frag.append(node)
        // L4: the conversation stopped right here (nothing came back, and
        // nothing is streaming right now) -- offer Retry on the spot.
        if (isLast && !state.streaming) {
          const row = document.createElement('div')
          row.className = 'assistant-acts'
          row.setAttribute('data-assistant-structural', '')
          const retryBtn = el('button', { class: 'btn quiet sm', type: 'button', 'data-assistant-retry': '', text: 'Retry' })
          retryBtn.addEventListener('click', () => retryLast())
          row.append(retryBtn)
          frag.append(row)
        }
        return
      }
      const acts = isLast && !state.streaming ? retryActsRow(() => retryLast()) : null
      const wrap = renderAssistantBubble(frag, m.content, { acts })
      wrap.setAttribute('data-assistant-structural', '')
      if (acts) acts.insertBefore(copyButton(m.content, 'Copy'), acts.firstChild)
    })
    if (state.streaming) {
      const wrap = renderAssistantBubble(frag, state.turnAcc, { thinking: !state.turnAcc })
      wrap.setAttribute('data-assistant-structural', '')
      wrap.setAttribute('data-assistant-live-turn', '')
    }
    // Structural content always goes back at the front, ahead of whatever
    // error lines already followed it -- those keep their place, in the
    // order they happened.
    thread.prepend(frag)
    scrollToBottom(thread)
  }
  // Patches just the live bubble's text on each delta -- avoids rebuilding
  // the whole thread (and losing scroll position) on every chunk. Falls
  // back to a full render if that node isn't there for some reason.
  function updateLiveBubble() {
    if (!state.panelEl) return
    const thread = state.panelEl.querySelector('[data-assistant-thread]')
    const wrap = thread && thread.querySelector('[data-assistant-live-turn]')
    if (!wrap) { renderThread(); return }
    const bubble = wrap.querySelector('.assistant-bubble')
    bubble.innerHTML = ''
    renderMarkdownInto(bubble, state.turnAcc, slug)
    scrollToBottom(thread)
  }

  async function runTurn() {
    const myTurn = ++state.turnId
    state.streaming = true
    state.turnAcc = ''
    renderThread()
    syncSendButton()
    const controller = new AbortController()
    state.abortController = controller
    const headers = { 'Content-Type': 'application/json' }
    const k = apiKeyHeader()
    if (k) headers['X-API-Key'] = k
    let res
    try {
      res = await fetch('/v1/hack/events/' + encodeURIComponent(slug) + '/assistant', {
        method: 'POST', headers,
        body: JSON.stringify({ messages: state.messages.map(m => ({ role: m.role, content: m.content })) }),
        signal: controller.signal
      })
    } catch (e) {
      if (state.turnId !== myTurn) return
      state.streaming = false
      renderThread(); syncSendButton()
      // Stop/Esc/Close aborts this same fetch -- that is a clean stop, not
      // a network failure, and reports no error line.
      if (!(e && e.name === 'AbortError')) appendErrorLine(state.panelEl && state.panelEl.querySelector('[data-assistant-thread]'), 'Cannot reach the server.')
      refocusAfterTurn()
      return
    }
    if (!res.ok) {
      if (state.turnId !== myTurn) return
      let data = {}
      try { data = await res.json() } catch (e) {}
      if (state.turnId !== myTurn) return
      state.streaming = false
      // M3/L4: a pre-stream refusal in the 400-499 range never produced any
      // answer, so the question that triggered it is popped back off
      // history -- otherwise the thread ends on an orphaned user turn that
      // can never get a reply, and a later send would pile a second one on
      // top of it. 400 specifically also hands the text back to the
      // textarea so it can be fixed and resent; the others (403/404/429)
      // aren't about the text itself, so there's nothing useful to restore.
      let poppedText = ''
      if (res.status >= 400 && res.status < 500 && state.messages.length && state.messages[state.messages.length - 1].role === 'user') {
        poppedText = state.messages[state.messages.length - 1].content
        state.messages = state.messages.slice(0, -1)
        saveHistory(key, state.messages)
      }
      renderThread(); syncSendButton()
      const thread = state.panelEl && state.panelEl.querySelector('[data-assistant-thread]')
      let msg, retry = true
      if (res.status === 400) {
        msg = cpLen(poppedText) > MAX_MSG_CHARS
          ? 'That question is too long. Shorten it and try again.'
          : ((data && typeof data.message === 'string' && data.message) || 'Something went wrong. Refresh the page and try again.')
        if (state.panelEl) {
          const ta = state.panelEl.querySelector('[data-assistant-input]')
          if (ta) { ta.value = poppedText; ta.dispatchEvent(new Event('input')) }
        }
      } else {
        msg = (data && typeof data.message === 'string' && data.message) || 'Something went wrong. Refresh the page and try again.'
      }
      if (data && (data.error === 'daily_limit' || data.error === 'monthly_budget')) { disableForLimit(msg); retry = false }
      if (data && typeof data.remaining_today === 'number') setRemaining(data.remaining_today)
      appendErrorLine(thread, msg, { retry })
      refocusAfterTurn()
      return
    }
    let acc = '', abortedByUser = false, midStreamError = '', doneReceived = false
    try {
      await readSSE(res, evt => {
        if (state.turnId !== myTurn) return
        if (typeof evt.delta === 'string') { acc += evt.delta; state.turnAcc = acc; updateLiveBubble() }
        else if (evt.done) { doneReceived = true; setRemaining(evt.remaining_today) }
        else if (evt.error) { midStreamError = evt.message || "The assistant couldn't answer. Try again." }
      })
    } catch (e) {
      if (e && e.name === 'AbortError') abortedByUser = true
      else midStreamError = midStreamError || 'Cannot reach the server.'
    }
    if (state.turnId !== myTurn) return
    // Stream ended with neither a done nor an error event -- the contract
    // promises one or the other, but never trust a network stream blindly.
    if (!doneReceived && !midStreamError && !abortedByUser) midStreamError = "The assistant couldn't answer. Try again."
    // L12: abort/failure without a done event means no fresh server count
    // arrived -- approximate the spend locally.
    if (!doneReceived && (abortedByUser || midStreamError)) bumpRemainingLocal()
    state.streaming = false
    state.abortController = null
    const text = acc.trim() ? acc : ''
    // Whitespace-only reply: not worth saving or showing as an answer.
    if (text) {
      state.messages.push({ role: 'assistant', content: acc })
      state.messages = capMessages(state.messages)
      saveHistory(key, state.messages)
    }
    state.turnAcc = ''
    renderThread()
    if (text && !abortedByUser) announce(acc)
    const thread = state.panelEl && state.panelEl.querySelector('[data-assistant-thread]')
    if (midStreamError) appendErrorLine(thread, midStreamError)
    syncSendButton()
    refocusAfterTurn()
  }

  function retryLast() {
    if (state.streaming) return
    if (!navigator.onLine) { appendErrorLine(state.panelEl && state.panelEl.querySelector('[data-assistant-thread]'), "You're offline. Reconnect and try again."); return }
    if (state.messages.length && state.messages[state.messages.length - 1].role === 'assistant') {
      state.messages = state.messages.slice(0, -1)
    }
    saveHistory(key, state.messages)
    runTurn()
  }

  function send(text) {
    if (!text || state.streaming) return
    hideChipsAndSub()
    state.messages.push({ role: 'user', content: text })
    state.messages = capMessages(state.messages)
    saveHistory(key, state.messages)
    runTurn()
  }

  function buildPanel() {
    const phone = isPhone()
    const panel = document.createElement('div')
    panel.id = 'assistant-panel-' + slug
    panel.className = 'assistant-panel' + (phone ? ' sheet' : '')
    // L8: fixed at build time, not recomputed on every keystroke -- a
    // window resize between opening and typing must not change which Tab
    // behaviour applies mid-session.
    panel.dataset.phone = phone ? '1' : ''
    panel.setAttribute('role', phone ? 'dialog' : 'complementary')
    if (phone) panel.setAttribute('aria-modal', 'true')
    panel.setAttribute('aria-label', 'Ask about this event')
    const head = el('div', { class: 'assistant-head' })
    head.append(el('h2', { text: 'Ask about this event' }))
    const acts = el('div', { class: 'assistant-head-acts' })
    const newChatBtn = el('button', { class: 'btn quiet sm', type: 'button', text: 'New chat' })
    const closeBtn = el('button', { class: 'icon-btn', type: 'button', 'data-assistant-close': '', 'aria-label': 'Close', text: '✕' })
    acts.append(newChatBtn, closeBtn)
    head.append(acts)
    panel.append(head)
    if (state.messages.length === 0) {
      panel.append(el('p', { class: 'assistant-sub small muted', 'data-assistant-sub': '', text: "Short answers, with a link to where to act. It can't change anything." }))
    }
    const thread = el('div', { class: 'assistant-thread', 'data-assistant-thread': '', 'aria-busy': state.streaming ? 'true' : 'false' })
    panel.append(thread)
    if (state.messages.length === 0 && !state.streaming) {
      const chips = el('div', { class: 'assistant-chips', 'data-assistant-chips': '' })
      starterChips(ev.stage, checklist).forEach(text => {
        const chip = el('button', { class: 'chip', type: 'button', text })
        chip.addEventListener('click', () => { trySend(text) })
        chips.append(chip)
      })
      panel.append(chips)
    }
    const compose = el('div', { class: 'assistant-compose' })
    const row = el('div', { class: 'assistant-input-row' })
    const ta = el('textarea', { class: 'input assistant-textarea', 'data-assistant-input': '', placeholder: 'Ask about this event…', rows: '1', maxlength: String(MAX_MSG_CHARS) })
    const sendBtn = el('button', { class: 'btn primary sm', type: 'button', 'data-assistant-send': '', text: 'Send' })
    sendBtn.disabled = true
    ta.value = state.draft || '' // a re-render keeps a half-typed question
    row.append(ta, sendBtn)
    compose.append(row)
    const hint = el('p', { class: 'small muted assistant-hint', 'data-assistant-hint': '' })
    hint.hidden = true
    compose.append(hint)
    panel.append(compose)
    panel.append(el('div', { class: 'sr-only', role: 'status', 'aria-live': 'polite', 'data-assistant-live': '' }))

    function grow() { ta.style.height = 'auto'; ta.style.height = ta.scrollHeight + 'px' }
    // L3: check online-ness, and let a 400/limit flow pop/restore text,
    // before the textarea is ever cleared -- an offline send must not lose
    // what was typed.
    function trySend(text) {
      if (!text || state.streaming) return
      if (!navigator.onLine) { appendErrorLine(thread, "You're offline. Reconnect and try again."); return }
      ta.value = ''; state.draft = ''; grow(); syncSendButton()
      send(text)
    }
    ta.addEventListener('input', () => { state.draft = ta.value; grow(); syncSendButton() })
    ta.addEventListener('keydown', e => {
      if (e.key === 'Enter' && !e.shiftKey && !e.isComposing && e.keyCode !== 229) {
        e.preventDefault()
        trySend(ta.value.trim())
      }
    })
    sendBtn.addEventListener('click', () => {
      if (state.streaming) { state.abortController?.abort(); return }
      trySend(ta.value.trim())
    })
    newChatBtn.addEventListener('click', () => {
      // M1: abort first, then bump the turn id so a stream already in
      // flight can never write its answer into the chat that replaces it.
      if (state.streaming) state.abortController?.abort()
      state.turnId++
      state.streaming = false
      state.abortController = null
      state.turnAcc = ''
      state.messages = []
      clearHistory(key)
      document.removeEventListener('keydown', onKeydown, true)
      panel.remove()
      state.panelEl = null
      openPanel()
    })
    closeBtn.addEventListener('click', () => {
      // M2: closing mid-stream aborts first (same as Esc) -- it does not
      // just hide a fetch that keeps running unseen. The partial reply is
      // still kept: runTurn's own abort handling above saves whatever text
      // had accumulated before the panel closes.
      if (state.streaming) state.abortController?.abort()
      closePanel()
    })
    // L7: on phone the sheet covers the whole screen, so a link inside a
    // reply (a /e/ or /docs/ link) has to close the sheet itself once
    // navigation is under way -- otherwise the sheet is left open over
    // whatever screen the click just navigated to.
    if (phone) {
      panel.addEventListener('click', e => {
        if (e.target.closest('a[href]')) closePanel()
      })
    }

    // NOT renderThread()/renderHint() here -- both resolve their target via
    // state.panelEl, which openPanel only assigns once this function
    // returns. Calling them this early would find state.panelEl still
    // pointing at whatever (if anything) was open before, and silently
    // render into that instead of this brand-new panel. openPanel calls
    // them itself right after the assignment.
    if (state.limitMessage) { ta.disabled = true; sendBtn.disabled = true }
    requestAnimationFrame(grow)
    return panel
  }

  function onKeydown(e) {
    // This mount's own screen is gone (a re-render happened) -- just drop
    // this stale listener. state.panelEl is shared across mounts (H2) and
    // may already belong to a newer mount's live panel by now, so this
    // must never null it out from here.
    if (ctx.alive && !ctx.alive()) { document.removeEventListener('keydown', onKeydown, true); return }
    if (!state.panelEl) return
    // M8: a confirm dialog or the guided tour owns the keyboard while
    // open -- same guard tour.js's own onKey uses for a confirm, extended
    // to the tour card too so the two overlays never fight over Esc/Tab.
    if (document.querySelector('.scrim') || document.querySelector('.tour-card')) return
    if (e.key === 'Escape') {
      e.preventDefault()
      if (state.streaming) { state.abortController?.abort(); return }
      closePanel()
      return
    }
    if (state.panelEl.dataset.phone === '1' && e.key === 'Tab') {
      const list = Array.from(state.panelEl.querySelectorAll(FOCUSABLE_SEL)).filter(x => !x.disabled && x.tabIndex !== -1 && !x.hidden)
      if (!list.length) return
      e.preventDefault()
      const idx = list.indexOf(document.activeElement)
      const next = idx === -1 ? 0 : (idx + (e.shiftKey ? list.length - 1 : 1)) % list.length
      list[next].focus()
    }
  }

  // state.panelEl can outlive the mount that built it -- a screen
  // re-render drops the old panel from the document (main.innerHTML
  // replaced) but nothing nulls the shared reference until a mount
  // actually deals with it. isConnected tells a real live panel apart
  // from a stale, detached one left over from before this mount started.
  function panelIsLive() { return !!(state.panelEl && state.panelEl.isConnected) }
  function openPanel() {
    if (panelIsLive()) return
    state.panelEl = buildPanel()
    container.append(state.panelEl)
    state.open = true
    askBtn.setAttribute('aria-expanded', 'true')
    document.addEventListener('keydown', onKeydown, true)
    // Now that state.panelEl actually points at this panel: fill in
    // whatever history/in-flight turn/limit state already exists (H2 --
    // this is the path that reopens a conversation that was already
    // happening, including on first open after a reload).
    renderThread()
    renderHint()
    state.panelEl.querySelector('[data-assistant-input]')?.focus()
  }
  function closePanel() {
    if (!panelIsLive()) { state.panelEl = null; return }
    document.removeEventListener('keydown', onKeydown, true)
    state.panelEl.remove()
    state.panelEl = null
    state.open = false
    askBtn.setAttribute('aria-expanded', 'false')
    if (askBtn.isConnected) askBtn.focus()
  }

  askBtn.addEventListener('click', () => { if (panelIsLive()) closePanel(); else openPanel() })

  // H2: a re-render (window focus on Overview, a stage change, coming back
  // to this screen) must reopen exactly the conversation that was already
  // happening, including a reply still streaming in -- not a blank panel.
  if (state.open) openPanel()
}
