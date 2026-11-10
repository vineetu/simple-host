import { navigation } from './navigation-data.js'

export function checklistActionLabel(item) {
  const verb = { 'j.rubric': 'Read', 'j.conflicts': 'Check', 'j.scores': 'Score' }[item.id]
  if (verb) return verb
  return navigation.targets.find(t => t.id === item.target_id)?.action_label || 'Open'
}

const blockedText = {
  event_taken_down: 'This event has been taken down.', entry_rules_closed: 'Entry rules are closed.', tie_rule_closed: 'Tie-break is closed.',
  judging_setup_locked: 'Move back to Entries closed to change this.', scores_locked: 'Scoring is locked.', judging_closed: 'Scoring is closed.',
  judging_not_open: 'Judging is not open yet.', nothing_assigned: 'No teams assigned yet.',
  join_team_first: 'Join a team first.', entry_not_ready: 'Finish the steps above first.', teams_locked: 'Teams are closed.',
  entry_unavailable: 'Entries are closed.', conduct_record_only: 'No acceptance recorded.'
}

// Server-selected guidance. Screens own their heroes and action confirmations.
export function renderChecklist({ ui, checklist, title = 'Checklist', renderAction, before = '', listStyle = '' }) {
  const card = ui.el('section', { class: 'card' })
  card.innerHTML = `<h2>${ui.esc(title)}</h2>${before}`
  let number = 0
  function list(items) {
    const ul = ui.el('ul', { class: 'steps' })
    if (listStyle) ul.setAttribute('style', listStyle)
    items.forEach(item => {
      number++
      const done = item.state === 'done'
      const opted = item.state === 'not_needed'
      const cls = [done ? 'done' : '', item.optional || item.settled ? 'opt' : '', !done && checklist.next?.item_id === item.id ? 'now' : ''].filter(Boolean).join(' ')
      const li = ui.el('li', { class: cls, 'data-checklist-id': item.id, ...(item.id === 'j.invite' ? { id: 'invite' } : item.id === 'j.coc' ? { id: 'conduct' } : item.id === 'j.closes' ? { id: 'closes' } : {}), tabindex: '-1' })
      const tick = done ? '<span class="tick">✓</span>' : `<span class="tick"><span class="num">${number}</span></span>`
      const detail = [item.optional && !done && !opted && item.id !== 'j.closes' ? 'Optional' : '', opted ? 'Not needed' : (item.detail || (!done && blockedText[item.blocked_reason]) || '')].filter(Boolean).join(' · ')
      li.innerHTML = tick + `<span class="what"><b>${ui.esc(item.label)}</b><span class="sub">${ui.esc(detail)}</span></span>`
      const actions = item.can_dismiss && !done ? ui.el('span', { class: 'step-actions act' }) : li
      if (!done && !opted && item.actionable) {
        const custom = renderAction?.(item)
        if (typeof custom === 'string') li.insertAdjacentHTML('beforeend', custom)
        else if (custom) li.append(custom)
        else if (!renderAction && item.actionable && item.link?.href) actions.append(ui.el('a', { class: `btn${checklist.next?.item_id === item.id ? ' primary' : ' quiet'} sm${item.can_dismiss ? '' : ' act'}`, href: item.link.href, text: checklistActionLabel(item) }))
      }
      if (item.can_dismiss && !done) { actions.append(ui.el('button', { class: 'btn quiet sm', type: 'button', 'data-checklist-mark': item.id, 'data-restore': String(opted), 'data-dismiss': '', text: opted ? 'Use this item' : 'Not needed' })); li.append(actions) }
      ul.append(li)
    })
    return ul
  }
  const items = checklist.items || []
  for (const [group, label] of [['earlier_required', 'Earlier required'], ['current', ''], ['earlier_optional', 'Earlier items'], ['history', 'History']]) {
    const rows = items.filter(i => (i.group || 'current') === group)
    if (!rows.length) continue
    if (!label) card.append(list(rows))
    else {
      const details = ui.el('details', {})
      details.open = group === 'earlier_required' && rows.some(i => i.actionable && i.state !== 'done')
      details.append(ui.el('summary', { text: label }), list(rows))
      card.append(details)
    }
  }
  return card
}

export function bindChecklist(node, { api, slug, refresh, alive = () => true, ui }) {
  const live = ui.live(node)
  node.querySelectorAll('[data-checklist-mark]').forEach(button => button.addEventListener('click', async () => {
    const id = button.dataset.checklistMark
    button.disabled = true
    try {
      const r = await api.api(button.dataset.restore === 'true' ? 'DELETE' : 'POST', `/v1/hack/events/${encodeURIComponent(slug)}/checklist/not-needed/${encodeURIComponent(id)}`)
      if (!alive()) return
      if (!r.ok) { button.disabled = false; live(ui.errText(r), 'err', button); return }
      await refresh()
      // refresh replaces the screen; locate the same row in the new tree.
      const row = document.querySelector(`[data-checklist-id="${id}"]`)
      if (row) {
        const details = row.closest('details'); if (details) details.open = true
        ;(row.querySelector('[data-checklist-mark]') || row).focus()
        ui.live(row.closest('main') || row)('Saved', 'ok')
      }
    } catch (_) { if (alive()) { button.disabled = false; live('Cannot reach the server.', 'err', button) } }
  }))
}

// Rubric acknowledgements bind to the version delivered with the displayed criteria.
export async function bindAcknowledgement(ctx, { node = ctx.main, id, label, role, after, version }) {
  const { ui, api, event } = ctx
  if (id !== 'j.conflicts' && !version) return
  const base = `/v1/hack/events/${encodeURIComponent(event.event.slug)}/checklist`
  const r = await api.api('GET', base + (role ? '?role=' + role : ''))
  if (ctx.alive && !ctx.alive()) return
  if (!r.ok) { ui.live(node)(ui.errText(r), 'err'); return }
  const item = r.data.checklist?.items.find(i => i.id === id) || (id === 'o.rubric-ready' ? r.data.checklist?.items.find(i => i.id === 'o.rubric') : null)
  if (!item || (item.id === id && item.state === 'done' && (!version || item.version === version)) || (!version && !item.version && id !== 'j.conflicts')) return
  const displayedVersion = version ?? item.version
  const button = ui.el('button', { class: 'btn', type: 'button', text: label })
  button.disabled = !!ui.writeLockReason(event.event, event.admin_view)
  node.append(button)
  button.addEventListener('click', async () => {
    button.disabled = true
    try {
      const result = await api.api('POST', base + '/acknowledgements/' + id, displayedVersion ? { version: displayedVersion } : {})
      if (ctx.alive && !ctx.alive()) return
      if (!result.ok) {
        ui.live(node)(ui.errText(result), 'err', button)
        if (result.data?.code === 'stale_checklist_version' && ctx.refresh) await ctx.refresh()
        else button.disabled = false
        return
      }
      button.textContent = 'Done'
      ui.live(node)('Saved', 'ok', button)
      if (after) await after()
    } catch (_) { button.disabled = false; ui.live(node)('Cannot reach the server.', 'err', button) }
  })
}
