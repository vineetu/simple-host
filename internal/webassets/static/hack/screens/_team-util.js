// Shared helpers for participant-team.js only (BUILD.md: "keep it inside
// your screen module or a new screens/_team-util.js" -- split out once the
// module passed ~400 lines). Not imported by any other screen: rowMenuHTML/
// wireRowMenus duplicate _orgA-util.js's organiser-batch versions on
// purpose rather than reaching across that file's own "only these screens
// import it" boundary (same small-duplication rule ui.js's own safeHref/
// markInk comments already use for _partB-util.js/_judge-util.js).
// row(): the plain two-line list row every team panel uses (name, a sub
// line, then whatever action markup the caller built).
export function row(esc, main_, sub, actionsHTML) {
  return '<div class="r"><div class="main"><b>' + esc(main_) + '</b><span>' + esc(sub) + '</span></div>' + (actionsHTML || '') + '</div>'
}

export const COPY_ICON = '<svg class="ico" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M7 7h9v9H7zM4 13V4h9"/></svg>'
export const PLUS_ICON = '<svg class="ico" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M10 4v12M4 10h12"/></svg>'
export const LOCK_ICON = '<svg class="ico" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M6 9V6.5a4 4 0 0 1 8 0V9M5 9h10v8H5z"/></svg>'
export const SAVED_ICON = '<svg class="ico" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M4 10l4 4 8-8"/></svg>'

// The team's own link, from `link` when the server already sends one, else
// built from the host and `code` -- and the same string with the scheme
// stripped, for display next to Copy (same split every addr card uses).
export function teamLink(team) {
  const link = (team && team.link) || (team && team.code ? location.origin + '/team/' + team.code : '')
  return { link, addr: link.replace(/^https?:\/\//, '') }
}

export function rowMenuHTML(esc, label, items) {
  let sawSep = false
  const body = items.map(it => {
    const hr = (it.danger || it.sep) && !sawSep ? '<hr>' : ''
    if (it.danger || it.sep) sawSep = true
    return hr + `<button type="button" role="menuitem"${it.disabled ? ' disabled' : ''}${it.danger ? ' class="danger"' : ''} data-act="${esc(it.act)}">${esc(it.label)}</button>`
  }).join('')
  return `<button class="icon-btn" type="button" data-rowmenu aria-haspopup="menu" aria-expanded="false" aria-label="More for ${esc(label)}"><svg class="ico" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="2.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M5 10h.01M10 10h.01M15 10h.01"/></svg></button><div class="row-menu" role="menu" hidden>${body}</div>`
}

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
  document.addEventListener('keydown', e => {
    if (e.key === 'Escape') {
      document.querySelectorAll('.row-menu:not([hidden])').forEach(m => {
        m.hidden = true
        const b = m.parentNode && m.parentNode.querySelector('[data-rowmenu]')
        if (b) { b.setAttribute('aria-expanded', 'false'); b.focus() }
      })
    }
  })
}
