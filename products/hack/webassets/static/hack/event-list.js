// Membership ordering is shared by the pane, switcher and full event list.
export function eventOrder(events, currentSlug) {
  return [...events].sort((a, b) => Number(b.slug === currentSlug) - Number(a.slug === currentSlug)
    || a.title.toLocaleLowerCase().localeCompare(b.title.toLocaleLowerCase()) || a.slug.localeCompare(b.slug))
}
export function pastOrder(events) {
  return [...events].sort((a, b) => (b.ended_at ? Date.parse(b.ended_at) : -Infinity) - (a.ended_at ? Date.parse(a.ended_at) : -Infinity)
    || a.title.toLocaleLowerCase().localeCompare(b.title.toLocaleLowerCase()) || a.slug.localeCompare(b.slug))
}
export function eventGroups(events, currentSlug) {
  const current = eventOrder(events.filter(e => e.stage !== 'archived' || e.slug === currentSlug), currentSlug)
  const past = pastOrder(events.filter(e => e.stage === 'archived' && e.slug !== currentSlug))
  return { current, past, pastCount: events.filter(e => e.stage === 'archived').length }
}
