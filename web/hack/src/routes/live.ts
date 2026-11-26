// Old and new side by side. The Go server serves this app for the screens
// it lists as live (screens.json, from every screens/<area>/live.json) and the
// old app for every other screen, and tells both apps which are live in
// <meta name="hack-live">. Moving between the two is a full page load; hop()
// is that load, with a guard so a disagreement can never loop.
import { resolveScreen } from "./resolve";

let cached: Set<string> | null | undefined;

/** liveScreens is the server's list, or null when no server sent one (the catalogue, vite dev). */
export function liveScreens(): Set<string> | null {
  if (cached !== undefined) return cached;
  const meta = document.querySelector('meta[name="hack-live"]');
  cached = meta ? new Set((meta.getAttribute("content") || "").split(/\s+/).filter(Boolean)) : null;
  return cached;
}

/** isHere is whether this app shows the screen at url (otherwise the old app does). */
export function isHere(url: string, registered: (id: string) => boolean): boolean {
  const u = new URL(url, location.href);
  if (u.origin !== location.origin) return false;
  const id = resolveScreen(u.pathname, u.search).id;
  const live = liveScreens();
  return registered(id) && (!live || live.has(id));
}

const HOP = "hack-hop";

/** hop loads url as a whole page (the other app), unless that same hop just happened (then it returns false). */
export function hop(url: string): boolean {
  const target = new URL(url, location.href).href;
  try {
    const last = JSON.parse(sessionStorage.getItem(HOP) || "null") as { to: string; at: number } | null;
    if (last && last.to === target && Date.now() - last.at < 5000) {
      sessionStorage.removeItem(HOP);
      return false;
    }
    sessionStorage.setItem(HOP, JSON.stringify({ to: target, at: Date.now() }));
  } catch {
    /* storage blocked: hop anyway */
  }
  location.assign(target);
  return true;
}

/** arrived clears the hop guard once a hop has landed where it meant to. */
export function arrived(): void {
  try {
    const last = JSON.parse(sessionStorage.getItem(HOP) || "null") as { to: string } | null;
    if (last && last.to === location.href) sessionStorage.removeItem(HOP);
  } catch {
    /* storage blocked */
  }
}
