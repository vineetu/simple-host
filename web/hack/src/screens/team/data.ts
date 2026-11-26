// The Team screen's plain logic (participant-team.js): what the person's team
// and the event's team finding look like, which ways onto a team are open,
// which one is picked first, and the "you are on this team now" note.
import type { EventDetail } from "@/lib/client";

export type Member = { user_id: string; display_name?: string; admin?: boolean; you?: boolean };
export type Track = { slug: string; name: string };
/** GET E's me.team, as the team screens read it. */
export type MyTeam = {
  slug: string;
  name: string;
  code?: string;
  link?: string;
  admin?: boolean;
  locked?: boolean;
  frozen?: boolean;
  looking_for?: string;
  members?: Member[];
  track?: Track | null;
  admin_successor?: string;
  pending_requests?: number;
  deadline?: string | null;
  site?: { url?: string } | null;
};
export type OpenTeam = { slug: string; name: string; member_count: number; team_size_max: number; track?: { name?: string } | null; members?: string[]; looking_for?: string; full?: boolean };
export type Pending = { id: string; kind: "invite" | "request"; team?: string; team_name?: string; user_id?: string; display_name?: string };
/** GET E/teams/open: team finding as this person sees it. */
export type Finding = { enabled: boolean; teams: OpenTeam[]; solo: { user_id: string; display_name: string; intro?: string }[]; pending: Pending[]; looking: boolean; intro: string };
export const NO_FINDING: Finding = { enabled: false, teams: [], solo: [], pending: [], looking: false, intro: "" };
export type Message = { id?: string; text: string; created_at: string; sender_name?: string; from_team?: boolean };

export type Way = "ask" | "code" | "start";
export const WAYS: Record<Way, [string, string]> = {
  ask: ["Ask to join a team", "Pick a team that's looking for people. Its admins say yes or no."],
  code: ["Join with a link or code", "A teammate sent you their team link or code. You join straight away."],
  start: ["Start a new team", "You'll be its admin. Share its link to bring people in."],
};

type Ev = EventDetail["event"] & { team_size_max?: number; tracks?: Track[] };
export const evOf = (d: EventDetail) => d.event as Ev;
export const teamOf = (d: EventDetail) => (d.me?.team as MyTeam | null | undefined) || null;

/** rules is what the person may do about teams now (the old screen's flags). */
export function rules(d: EventDetail, now = Date.now()) {
  const ev = evOf(d);
  const team = teamOf(d);
  const deadlinePassed = !!(ev.submission_deadline && new Date(ev.submission_deadline).getTime() <= now);
  const teamsOpen = (ev.stage === "open" || ev.stage === "building") && !ev.taken_down;
  const ended = ev.stage === "archived";
  const frozen = !!ev.taken_down || (team ? !!team.frozen : ev.stage === "closed");
  const size = ev.team_size_max || 0;
  return {
    teamsOpen,
    ended,
    frozen,
    canStart: teamsOpen && !frozen && !deadlinePassed,
    canJoinByCode: teamsOpen && !ended,
    canDiscover: teamsOpen && !deadlinePassed,
    isAdmin: !!team?.admin,
    canAct: !!team && !frozen,
    canInvite: !!team && !frozen && !team.locked,
    full: !!team && size > 0 && (team.members || []).length >= size,
    size,
  };
}
export type Rules = ReturnType<typeof rules>;

/** ways are the open ways onto a team, in the screen's order. */
export function ways(r: Rules, f: Finding): Way[] {
  const out: Way[] = [];
  if (f.enabled && r.canDiscover) out.push("ask");
  if (r.canJoinByCode) out.push("code");
  if (r.canStart) out.push("start");
  return out;
}

/** firstWay is the way picked when the page opens: Ask when someone is looking (or a request is out), else Start. */
export function firstWay(open: Way[], f: Finding): Way {
  if (!f.enabled) return open.includes("code") ? "code" : "start";
  if (open.includes("ask") && (f.teams.length || f.pending.some((p) => p.kind === "request"))) return "ask";
  return open.includes("start") ? "start" : open[0];
}

/** inviteLine: "Invited you · 2 of 4 · Good Health" when the team is in the looking list. */
export function inviteLine(p: Pending, f: Finding): string {
  const t = f.teams.find((x) => x.slug === p.team);
  if (!t) return "Invited you to join";
  return "Invited you · " + t.member_count + " of " + t.team_size_max + (t.track?.name ? " · " + t.track.name : "");
}

/** openLine: "2 of 4 · Food · Dewi, Rina · Looking for: a designer". */
export function openLine(t: OpenTeam): string {
  const members = (t.members || []).filter(Boolean);
  return (
    t.member_count +
    " of " +
    t.team_size_max +
    (t.track?.name ? " · " + t.track.name : "") +
    (members.length ? " · " + members.join(", ") : "") +
    (t.looking_for && !t.full ? " · Looking for: " + t.looking_for : "")
  );
}

/** teamLink is the team's link (the server's, else built from its code) and the same without the scheme. */
export function teamLink(team: MyTeam | null, origin = location.origin) {
  const link = team?.link || (team?.code ? origin + "/team/" + team.code : "");
  return { link, addr: link.replace(/^https?:\/\//, "") };
}

// "Just placed": no server field says an organiser's Fill teams put someone
// on a team, so the screen compares with the team it saw last time
// (localStorage 'sh-team-baseline-<user>-<event>', shared with the old Home),
// and a join or start the person did themselves is marked just before
// (sessionStorage 'sh-selfjoin-<event>').
export type Placed = "" | "joined" | "started" | "placed";

function read(s: Storage | undefined, k: string): string | null {
  try {
    return s ? s.getItem(k) : null;
  } catch {
    return null;
  }
}
function write(s: Storage | undefined, k: string, v: string | null) {
  try {
    if (!s) return;
    if (v === null) s.removeItem(k);
    else s.setItem(k, v);
  } catch {
    /* storage blocked */
  }
}

const seen = new Map<string, Placed>();

/** justPlaced reads and moves the baseline once per page load for each team the person is seen on. */
export function justPlaced(uid: string, slug: string, teamSlug: string, store = { local: globalThis.localStorage, session: globalThis.sessionStorage }): Placed {
  const memo = uid + "|" + slug + "|" + teamSlug;
  if (seen.has(memo)) return seen.get(memo)!;
  const selfKey = "sh-selfjoin-" + slug;
  const self = read(store.session, selfKey) || "";
  write(store.session, selfKey, null);
  const baseKey = "sh-team-baseline-" + uid + "-" + slug;
  const baseline = read(store.local, baseKey);
  write(store.local, baseKey, teamSlug);
  let out: Placed = "";
  if (teamSlug) {
    if (self === "joined" || self === "started") out = self;
    else if (baseline !== null && baseline !== teamSlug) out = "placed";
  }
  seen.set(memo, out);
  return out;
}

export function markSelfJoin(slug: string, kind: "joined" | "started") {
  write(globalThis.sessionStorage, "sh-selfjoin-" + slug, kind);
}

/** placedNote is the one-time line on Members. */
export function placedNote(p: Placed, name: string): string {
  return { "": "", joined: "You joined " + name + ".", started: "You started " + name + ". Copy its link to bring people in.", placed: "You are on " + name + " now." }[p];
}

/** tabs are the on-team sub-pages (Find people only with team finding on). */
export function tabsFor(f: Finding): ("members" | "people" | "settings")[] {
  return f.enabled ? ["members", "people", "settings"] : ["members", "settings"];
}
