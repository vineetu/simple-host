// Teams and Team detail: the payloads they read (GET E/teams, E/teams/open,
// E/teams/fill, E/entries, E/teams/{t}/messages) and the plain logic behind
// their words (organiser-teams.js, organiser-team-detail.js, _team-message-dialog.js).

export type Member = { user_id: string; display_name?: string; email?: string; admin?: boolean };
export type Team = {
  id: string;
  slug: string;
  name: string;
  code?: string;
  link?: string;
  locked?: boolean;
  kept?: boolean;
  track?: { slug?: string; name?: string } | null;
  site?: { exists?: boolean; taken_down?: boolean } | null;
  members: Member[];
};
export type Solo = { user_id: string; email?: string; display_name?: string };
export type TeamList = { teams?: Team[]; team_size_max?: number; on_no_team?: Solo[] };
export type OpenTeams = { solo?: { user_id: string; intro?: string }[] };
export type PlanRow = { team: string; name: string; new?: boolean; members: { user_id: string; display_name?: string }[] };
export type FillAnswer = { plan?: PlanRow[]; skipped?: { name: string }[]; size?: number; applied?: boolean };
export type EntryRow = { team: { slug: string }; missing?: string[] };
export type Entries = { required?: string[]; entries?: EntryRow[] };
export type Message = { id?: string; text: string; sender_name?: string; from_team?: boolean; created_at?: string };
export type Sent = { messages?: { recipients?: number }[] };

export const MAX_MESSAGE = 1000;
export const MAX_SELECTED = 25;

export const personName = (p: { display_name?: string; email?: string }) => p.display_name || p.email || "Someone";

/** plural: "1 team", "3 teams". */
export const plural = (n: number, one: string, many = one + "s") => n + " " + (n === 1 ? one : many);

/** joinAnd: "A", "A and B", "A, B and C" ("" for none). */
export function joinAnd(items: string[]): string {
  if (!items.length) return "";
  if (items.length === 1) return items[0];
  return items.slice(0, -1).join(", ") + " and " + items[items.length - 1];
}

/** The Teams heading's line: "6 teams · 2 people not on a team". */
export function teamsSubtitle(teams: number, solo: number): string {
  return plural(teams, "team") + " · " + (solo ? plural(solo, "person", "people") + " not on a team" : "Everyone is on a team");
}

/** adminsLine: "Admin: Ayu" or "Admins: Ayu, Nadia" ("" when none). */
export function adminsLine(t: Team): string {
  const admins = t.members.filter((m) => m.admin).map((m) => m.display_name || "Someone");
  if (!admins.length) return "";
  return (admins.length === 1 ? "Admin: " : "Admins: ") + admins.join(", ");
}

/** roomFor is the teams a person can be placed on: room left and not kept after everyone left (a lock does not stop the organiser). */
export const roomFor = (teams: Team[], max: number, except?: string) => teams.filter((t) => t.slug !== except && t.members.length < max && !t.kept);

/** planSignature compares two fill plans, ignoring order. */
export function planSignature(plan: PlanRow[] | undefined): string {
  return JSON.stringify(
    (plan || []).map((p) => ({ team: p.team, members: p.members.map((m) => m.user_id).slice().sort() })).sort((a, b) => a.team.localeCompare(b.team)),
  );
}

export const placedCount = (plan: PlanRow[]) => plan.reduce((s, p) => s + p.members.length, 0);

/** fillLabel is the Fill dialog's button: "Place 7 people", or "Nothing to place". */
export function fillLabel(plan: PlanRow[]): string {
  const n = placedCount(plan);
  return n ? "Place " + plural(n, "person", "people") : "Nothing to place";
}

/** fillSize is the typed "people per team" when it is a whole number from 1 to the team size, else 0. */
export function fillSize(value: string, max: number): number {
  const n = Number(value);
  return value.trim() !== "" && Number.isInteger(n) && n >= 1 && n <= max ? n : 0;
}

const FIELD_LABEL: Record<string, string> = {
  title: "project name",
  problem: "the problem",
  tagline: "the solution",
  audience: "who it's for",
  track: "a track",
  tools: "tools and AI",
  maturity: "what works today",
  video_url: "a video link",
  site: "a published website",
  code_url: "a code link",
  screenshot: "a screenshot",
  description: "a description",
  one_of: "a video or website",
};

export type Pill = { tone: "ok" | "down" | "neutral"; text: string };

/** entryPill: what the team's entry still misses against the event's rules. */
export function entryPill(entry: EntryRow | null | undefined, required: string[]): Pill {
  if (!entry || !required.length) return { tone: "ok", text: "No fields required" };
  const missing = entry.missing || [];
  if (!missing.length) return { tone: "ok", text: "Complete" };
  return { tone: "down", text: "Missing: " + joinAnd(missing.map((f) => FIELD_LABEL[f] || f)) };
}

export function sitePill(t: Team): Pill {
  if (!t.site || !t.site.exists) return { tone: "neutral", text: "Not published" };
  return t.site.taken_down ? { tone: "down", text: "Taken down" } : { tone: "ok", text: "Live" };
}

/** agoText: "just now", "3 hours ago", "2 days ago" ("" for the future or nothing). */
export function agoText(iso: string | undefined, now = Date.now()): string {
  if (!iso) return "";
  const diff = now - new Date(iso).getTime();
  if (!Number.isFinite(diff) || diff < 0) return "";
  if (diff < 60000) return "just now";
  const m = Math.floor(diff / 60000);
  if (m < 60) return plural(m, "minute") + " ago";
  const h = Math.floor(m / 60);
  if (h < 24) return plural(h, "hour") + " ago";
  return plural(Math.floor(h / 24), "day") + " ago";
}

/** sentTo is how many people a send reached, summed over the teams. */
export const sentTo = (r: Sent) => (r.messages || []).reduce((s, m) => s + (m.recipients || 0), 0);

/** teamLink is the team's join link as shown (no scheme). */
export const linkText = (t: Team, origin: string) => (t.link || (t.code ? origin + "/team/" + t.code : "")).replace(/^https?:\/\//, "");
export const teamLinkUrl = (t: Team, origin: string) => t.link || (t.code ? origin + "/team/" + t.code : "");
