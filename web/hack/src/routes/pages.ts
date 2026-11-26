// This app serves every signed-in and link address; the pages the Go server
// writes (home, /directory, the public event page, marketing) are not here.
// Moving to one of those is a full page load; hop() is that load, with a
// guard so a disagreement between the two can never loop.
import { resolveScreen } from "./resolve";

/** isHere is whether this app shows the screen at url (otherwise the server writes the page). */
export function isHere(url: string, registered: (id: string) => boolean): boolean {
  const u = new URL(url, location.href);
  if (u.origin !== location.origin) return false;
  return registered(resolveScreen(u.pathname, u.search).id);
}

const HOP = "hack-hop";

/** hop loads url as a whole page, unless that same hop just happened (then it returns false). */
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
