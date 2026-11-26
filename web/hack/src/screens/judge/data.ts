// The judge's screens' plain logic and the payloads they read (the old
// _judge-util.js, judge-home/queue/scoring/rubric.js decisions, same words).
import type { EventDetail } from "@/lib/client";
import type { EntryCard } from "@/lib/client";
import { safeHref } from "@/lib/labels";

// ---- Payloads (GET E/judge/queue, E/rubric, E/conflicts, E/judge/scores/{t}, E/checklist) ----

export type QueueRow = EntryCard & {
  team_id: string;
  team: { id?: string; slug: string; name: string };
  description?: string;
  done?: boolean;
  incomplete?: boolean;
  submitted_at?: string | null;
  updated_at?: string | null;
  video?: { url: string; host?: string; embed_url?: string | null } | null;
  site?: { url?: string; preview_url?: string | null; host?: string; frameable?: boolean; not_frameable_why?: string | null } | null;
  site_url?: string;
  site_exists?: boolean;
  pinned_url?: string | null;
  pinned_version?: number | null;
};
export type Candidate = { team_id: string; team_name: string; conflicted?: boolean };
export type Queue = { queue: QueueRow[]; candidates?: Candidate[]; judge_first?: string; assignment_mode?: string };
export type Criterion = { id: string; name: string; description?: string; max_points: number; weight?: number | null };
export type Rubric = { criteria: Criterion[]; version?: string };
export type Conflict = { team_id: string; team_name: string; had_score?: boolean };
export type Scores = { scores: { criterion_id: string; points: number }[]; comment?: string; complete?: boolean };
export type ChecklistItem = {
  id: string;
  label: string;
  state?: "done" | "not_needed" | string;
  detail?: string;
  optional?: boolean;
  settled?: boolean;
  blocked_reason?: string;
  actionable?: boolean;
  link?: { href: string } | null;
  can_dismiss?: boolean;
  group?: string;
  target_id?: string;
  version?: string;
};
export type Checklist = { items: ChecklistItem[]; next?: { kind?: string; item_id?: string; label?: string; link?: { href: string } | null } | null };

/** eventFlags are the three facts every judge screen reads off the event. */
export function eventFlags(ev: EventDetail["event"]) {
  const locked = !!ev.judging_locked_at;
  const resultsOut = !!ev.results_published && !ev.scoring_open;
  const notOpen = !ev.scoring_open && !locked && !resultsOut;
  return { locked, resultsOut, notOpen };
}

export const teamWord = (n: number) => (n === 1 ? "team" : "teams");

// ---- Overview ----

export type HomeTop =
  | { kind: "results" }
  | { kind: "not-open" }
  | { kind: "nothing" }
  | { kind: "all-conflicted" }
  | { kind: "locked-done"; scored: number; total: number }
  | { kind: "locked-partial"; scored: number; total: number; left: number }
  | { kind: "done"; scored: number; total: number }
  | { kind: "next"; scored: number; total: number; team: string };

/** homeTop is what Overview leads with (judge-home.js, same order of checks). */
export function homeTop(ev: EventDetail["event"], q: Queue): HomeTop {
  const { locked, resultsOut } = eventFlags(ev);
  const queue = q.queue || [];
  const total = queue.length;
  const scored = queue.filter((r) => r.done).length;
  const hasCandidates = (q.candidates?.length || 0) > 0;
  if (resultsOut) return { kind: "results" };
  if (!ev.scoring_open && !locked) return { kind: "not-open" };
  if (total === 0) return hasCandidates ? { kind: "all-conflicted" } : { kind: "nothing" };
  if (locked) return scored >= total ? { kind: "locked-done", scored, total } : { kind: "locked-partial", scored, total, left: total - scored };
  if (scored >= total) return { kind: "done", scored, total };
  return { kind: "next", scored, total, team: queue.find((r) => !r.done)!.team_id };
}

/** setupStep is the checklist step that comes before scoring (read the rubric, check who you know), if one is next (UX-1 S1). */
export function setupStep(c: Checklist | undefined): (ChecklistItem & { href: string }) | null {
  const next = c?.next;
  if (!next || next.kind !== "link" || next.item_id === "j.scores" || !next.link?.href) return null;
  const item = (c.items || []).find((i) => i.id === next.item_id) || ({ id: next.item_id!, label: next.label || "" } as ChecklistItem);
  return { ...item, label: next.label || item.label, href: next.link.href };
}

const VERB: Record<string, string> = { "j.rubric": "Read", "j.conflicts": "Check", "j.scores": "Score" };
/** actionLabel is a checklist item's button word (checklist.js checklistActionLabel). */
export function actionLabel(item: ChecklistItem, targets: readonly { id: string; action_label?: string | null }[]): string {
  return VERB[item.id] || targets.find((t) => t.id === item.target_id)?.action_label || "Open";
}

export const BLOCKED: Record<string, string> = {
  event_taken_down: "This event has been taken down.",
  entry_rules_closed: "Entry rules are closed.",
  tie_rule_closed: "Tie-break is closed.",
  judging_setup_locked: "Move back to Entries closed to change this.",
  scores_locked: "Scoring is locked.",
  judging_closed: "Scoring is closed.",
  judging_not_open: "Judging is not open yet.",
  nothing_assigned: "No teams assigned yet.",
  join_team_first: "Join a team first.",
  entry_not_ready: "Finish the steps above first.",
  teams_locked: "Teams are closed.",
  entry_unavailable: "Entries are closed.",
  conduct_record_only: "No acceptance recorded.",
};

/** stepDetail is the line under a checklist row. */
export function stepDetail(item: ChecklistItem): string {
  const done = item.state === "done";
  const opted = item.state === "not_needed";
  return [item.optional && !done && !opted && item.id !== "j.closes" ? "Optional" : "", opted ? "Not needed" : item.detail || (!done && item.blocked_reason ? BLOCKED[item.blocked_reason] || "" : "")]
    .filter(Boolean)
    .join(" · ");
}

/** leaveLine is the danger zone's sentence, and whether Leave can be pressed (judge-home.js judgeDangerZone). */
export function leaveLine(p: { blocked?: { code?: string; message?: string } | null; scored_teams?: number }, organiser: boolean): { text: string; disabled: boolean } {
  if (p.blocked) {
    const code = p.blocked.code;
    return {
      disabled: true,
      text:
        code === "event_closed"
          ? "This event has ended, so nobody can leave it. It stays on your Home as a record."
          : code === "event_taken_down"
            ? "This event has been taken down."
            : p.blocked.message || "You can't leave this event right now.",
    };
  }
  if (organiser) return { disabled: false, text: "You'll leave as an organiser. Review what happens before leaving." };
  const n = p.scored_teams || 0;
  return { disabled: false, text: n > 0 ? `You'll lose access to judging. Your scores for ${n} team${n === 1 ? "" : "s"} stay and still count in the results.` : "You'll lose access to judging." };
}

/** splitNeed is a Needs you line's title and the short line under it (needs.js). */
export function splitNeed(item: { kind: string; text: string; section?: string }): [string, string] {
  const OPEN: Record<string, string> = { team: "Open Team", website: "Open Website", entry: "Open Entry", people: "Review applications", home: "", queue: "Open My teams" };
  const text = String(item.text || "");
  if (item.kind === "team_message") return [text, OPEN[item.section || ""] || ""];
  const i = text.indexOf(". ");
  if (i > 0) return [text.slice(0, i + 1), text.slice(i + 2)];
  return [text, OPEN[item.section || ""] || ""];
}

// ---- My teams ----

/** hostLabel is a link's host name ("youtu.be"), or "". */
export function hostLabel(url?: string | null): string {
  const h = safeHref(url || "");
  if (!h) return "";
  try {
    return new URL(h).hostname;
  } catch {
    return "";
  }
}

/** siteHref is the team's site to open: the pinned snapshot wins over the live site. */
export function siteHref(row: QueueRow): string {
  if (row.pinned_url) return safeHref(row.pinned_url);
  if (row.site_exists && row.site_url) return safeHref(row.site_url);
  return "";
}

/** emptyToScore is what To score says when nothing is left in it (judge-queue.js). */
export function emptyToScore(q: Queue, scoredCount: number): string {
  if (scoredCount) return "Nothing left — you’ve scored everyone assigned to you.";
  if ((q.candidates?.length || 0) > 0 && !(q.queue || []).length) return "Nothing to score — you know someone on every team on your list.";
  if (q.assignment_mode && q.assignment_mode !== "open") return "Nothing to score yet. No team with an entry is on your list.";
  return "Nothing to score yet. No team has an entry to judge.";
}

/** knownList is every team to look over for "Teams I know": the candidates (or the queue), plus own conflicts. */
export function knownList(q: Queue, conflicts: Conflict[]): { team_id: string; team_name: string; mine?: Conflict }[] {
  const base: { team_id: string; team_name: string }[] = q.candidates || (q.queue || []).map((r) => ({ team_id: r.team_id, team_name: r.team.name }));
  const all = new Map<string, { team_id: string; team_name: string }>();
  for (const c of [...base, ...conflicts]) all.set(c.team_id, { team_id: c.team_id, team_name: c.team_name });
  return [...all.values()].map((c) => ({ ...c, mine: conflicts.find((x) => x.team_id === c.team_id) }));
}

export const rubricMax = (criteria: Criterion[] | undefined) => (criteria || []).reduce((s, c) => s + (Number(c.max_points) || 0), 0);
export const pointsOf = (s: Scores | undefined) => (s?.scores || []).reduce((sum, x) => sum + (Number(x.points) || 0), 0);

/** queueTabs picks the open tab: the asked one, else the first with something in it. */
export function openTab(asked: string | null, counts: Record<"to-score" | "scored" | "conflicts", number>): "to-score" | "scored" | "conflicts" {
  const ids = ["to-score", "scored", "conflicts"] as const;
  return ids.find((t) => t === asked) || ids.find((t) => counts[t] > 0) || "to-score";
}

// ---- Scoring ----

/** walkOrder keeps this judge's own order for the session: the queue's order reshuffles as anyone scores. */
export function walkOrder(stored: unknown, queue: QueueRow[]): string[] {
  const present = new Set(queue.map((r) => r.team_id));
  const order = (Array.isArray(stored) ? stored : []).filter((id): id is string => typeof id === "string" && present.has(id));
  for (const r of queue) if (!order.includes(r.team_id)) order.push(r.team_id);
  return order;
}

/** pickTeam is the team to open when the address names none (or one no longer on the list). */
export function pickTeam(asked: string, queue: QueueRow[]): string {
  if (asked && queue.some((r) => r.team_id === asked)) return asked;
  return (queue.find((r) => !r.done) || queue[0])?.team_id || "";
}

/** weightPct is a criterion's weight as a share of the whole rubric ("40%"), or "". */
export function weightPct(c: Criterion, all: Criterion[]): string {
  const sum = all.reduce((s, x) => s + (Number(x.weight) || 0), 0);
  const w = Number(c.weight);
  if (!sum || c.weight == null || Number.isNaN(w)) return "";
  return Math.round((w / sum) * 100) + "%";
}

/** guardLine is the one line Save & next shows when criteria have no score yet. */
export function guardLine(unscored: string[]): string {
  if (!unscored.length) return "";
  const list = unscored.length === 1 ? unscored[0] : unscored.slice(0, -1).join(", ") + " and " + unscored[unscored.length - 1];
  return `${list} ${unscored.length === 1 ? "has" : "have"} no score yet. Press again to move on.`;
}

const PLAYERS = /^https:\/\/(www\.youtube-nocookie\.com\/embed\/|player\.vimeo\.com\/video\/|www\.loom\.com\/embed\/|drive\.google\.com\/file\/d\/)/;
/** embedURL is the server-built player address, only when it is one of the four fixed players. */
export function embedURL(row: QueueRow): string {
  const v = row.video;
  return v && safeHref(v.url) && typeof v.embed_url === "string" && PLAYERS.test(v.embed_url) ? v.embed_url : "";
}

/** framedURL is the server-issued preview, only when the site may be framed and it is on the site's own host. */
export function framedURL(row: QueueRow): string {
  const s = row.site;
  if (!s || !s.frameable || !s.preview_url || !safeHref(s.preview_url) || !/^https:/.test(s.preview_url)) return "";
  return hostLabel(s.preview_url) === String(s.host || "").toLowerCase() ? s.preview_url : "";
}

export const WHY: Record<string, string> = {
  passcode: "This site asks visitors for a passcode, so it can’t show here.",
  viewers: "Only people the team named can open this site, so it can’t show here.",
  offline: "The team took this site offline.",
  taken_down: "The organiser took this site down.",
  not_ready: "This site can’t show here.",
};

/** onJudgeDocument is whether this page was loaded at a judge address, whose policy lets the site and video frames load. */
export function onJudgeDocument(firstURL: string | undefined): boolean {
  if (!firstURL) return true;
  try {
    return /^\/e\/[^/]+\/judge\/?$/.test(new URL(firstURL).pathname);
  } catch {
    return true;
  }
}

/** paragraphs splits free text on blank lines. */
export function paragraphs(text: unknown): string[] {
  return String(text || "")
    .split(/\n{2,}|\r\n\r\n/)
    .map((s) => s.trim())
    .filter(Boolean);
}
