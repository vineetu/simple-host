// Which screen a URL is: the old app's nav.js resolveRoute, on the same
// navigation map (navigation.gen.ts). Screen ids are the map's (and the UX
// atlas's). The Go server resolves the same way (products/hack/pages/hack_ui.go);
// resolve-cases.json is the table both are tested against.
import { navigation } from "@/navigation.gen";
import type { Role } from "@/lib/client";

export type Match = { id: string; slug?: string; role?: Role; section?: string; code?: string };

const AREA: Record<string, Role> = { manage: "organiser", team: "participant", judge: "judge" };
const SEGMENT: Record<Role, string> = { organiser: "manage", participant: "team", judge: "judge" };
const EXTRA: Record<Role, string[]> = { organiser: ["team", "entry", "assignments"], participant: [], judge: ["scoring"] };
const ALIAS: Record<string, string> = {
  "organiser-team": "organiser-team-detail",
  "organiser-entry": "organiser-entry-detail",
  "organiser-assignments": "organiser-judging-assignments",
};
const INVITE: Record<string, string> = { join: "public-join", judge: "public-judge", organiser: "public-organiser-invite", team: "public-team-link" };

/** sections are an area's ?s= values, its default first. */
export function sections(role: Role): string[] {
  const nav = navigation.roles.find((r) => r.id === role)!.nav.map((n) => n.id as string);
  return [...nav, ...EXTRA[role]];
}

export function resolveScreen(pathname: string, search = ""): Match {
  const path = pathname.replace(/\/+$/, "");
  if (path === "/signin") return { id: "signin" }; // the old app's sign-in page
  if (path === "/events/new") return { id: "organiser-create-event" };
  if (path === "/account") return { id: "account" };
  const invite = path.match(/^\/(join|judge|organiser|team)\/([^/]+)$/);
  if (invite) return { id: INVITE[invite[1]], code: decodeURIComponent(invite[2]) };
  const apply = path.match(/^\/e\/([^/]+)\/apply$/);
  if (apply) return { id: "public-apply", slug: decodeURIComponent(apply[1]) };
  const m = path.match(/^\/e\/([^/]+)\/(manage|team|judge)$/);
  if (!m) return { id: "landing-your-events" };
  const role = AREA[m[2]];
  const all = sections(role);
  const wanted = new URLSearchParams(search).get("s") || "";
  const section = all.includes(wanted) ? wanted : all[0];
  const id = role + "-" + section;
  return { id: ALIAS[id] || id, section, slug: decodeURIComponent(m[1]), role };
}

/** href is an event area's address: /e/{slug}/{manage|team|judge}?s=section. */
export function href(role: Role, slug: string, section?: string): string {
  return "/e/" + encodeURIComponent(slug) + "/" + SEGMENT[role] + (section ? "?s=" + encodeURIComponent(section) : "");
}

/** home is where an event opens for its role. */
export const eventHome = (role: Role, slug: string) => href(role, slug, role === "organiser" ? "overview" : "home");

/** needHref is the screen a to-do opens, inside the person's own area (needs.js). */
export function needHref(role: Role, slug: string, item: { section?: string; team?: string }): string {
  const section = item.section === "home" ? undefined : item.section;
  const base = href(role, slug, section);
  return item.team ? base + (base.includes("?") ? "&" : "?") + "team=" + encodeURIComponent(item.team) : base;
}
