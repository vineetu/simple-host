// The order of the person's events, shared by Your events and the rail (event-list.js).
import type { EventRow } from "@/lib/client";

const byTitle = (a: EventRow, b: EventRow) => a.title.toLocaleLowerCase().localeCompare(b.title.toLocaleLowerCase()) || a.slug.localeCompare(b.slug);

/** eventGroups splits events into current (the open one first, then by title) and past (ended, latest first). */
export function eventGroups(events: EventRow[], currentSlug?: string) {
  const current = events
    .filter((e) => e.stage !== "archived" || e.slug === currentSlug)
    .sort((a, b) => Number(b.slug === currentSlug) - Number(a.slug === currentSlug) || byTitle(a, b));
  const ended = (e: EventRow) => (e.ended_at ? Date.parse(e.ended_at) : -Infinity);
  const past = events.filter((e) => e.stage === "archived" && e.slug !== currentSlug).sort((a, b) => ended(b) - ended(a) || byTitle(a, b));
  return { current, past, pastCount: events.filter((e) => e.stage === "archived").length };
}
