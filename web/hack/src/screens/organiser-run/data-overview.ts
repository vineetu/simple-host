// Home (organiser Overview) and Results: the plain logic, with the old
// screens' exact words (organiser-overview.js, _orgA-util.js,
// _publish-results.js, stage-announce.js, schedule.js cueHeading,
// checklist.js). No React here; data-overview.test.ts covers it.
import { dateTime } from "@/lib/time";
import { navigation } from "@/navigation.gen";
import type { EventDetail } from "@/lib/client";

export const STAGE_ORDER = ["draft", "open", "building", "closed", "judging", "results", "archived"] as const;

// ---- what GET E says to the organiser --------------------------------------

export type Cue = { key?: string; title?: string; start_at?: string; now?: boolean };
export type Step = {
  kind: "none" | "stage" | "link" | "entry_rules" | "publish_results" | string;
  label?: string;
  stage?: string;
  item_id?: string;
  target_id?: string;
  link?: { href: string } | null;
  deadline_passed?: boolean;
  blocked?: string | null;
  blocked_reason?: string | null;
  cue?: Cue | null;
};
export type ChecklistItem = {
  id: string;
  label: string;
  state: "done" | "todo" | "not_needed" | string;
  optional?: boolean;
  can_dismiss?: boolean;
  group?: "earlier_required" | "current" | "earlier_optional" | "history" | string;
  actionable?: boolean;
  target_id?: string;
  link?: { href: string } | null;
  detail?: string;
  settled?: boolean;
  blocked_reason?: string | null;
};
export type Checklist = { role?: string; stage?: string; items?: ChecklistItem[]; next?: Step | null };
export type Counts = { participants?: number; teams?: number; judges?: number; on_no_team?: number; emailable_participants?: number };
export type OrgView = EventDetail & {
  organiser?: { join_url?: string; judge_url?: string; counts?: Counts };
  next_step?: Step | null;
  checklist?: Checklist | null;
};

// ---- the one next step -----------------------------------------------------

/** What the next-step card says: the heading, the sentence (with an optional link), the button, and the confirm's words. */
export type Content = {
  h2: string;
  sentence: string;
  /** A link inside the sentence: [before, link text, href, after]. */
  link?: [string, string, string, string];
  button: string | null;
  confirmBody?: string;
  navHref?: string;
  warn?: boolean;
  blocked?: boolean;
  isEnd?: boolean;
  cueNote?: string;
};

export const MOVES: Record<string, string> = {
  open: "Open sign-up",
  building: "Start building",
  closed: "Close entries",
  judging: "Start judging",
  results: "Finish judging",
  publish_results: "Publish results",
};

// What a forward (or sideways) move to each stage means for participants and judges.
export const FORWARD_EFFECT: Record<string, string> = {
  open: "Sign-up opens. Participants can join and apply, and judges can use their invite link.",
  building: "Entries open. Teams can edit their entry and website until you close entries.",
  closed: "Entries close for every team now, except teams with their own extension. Set up judging now; judges can score once you start judging.",
  judging:
    "Teams can no longer edit their entry, team or website. Judges can score. Locked until you go back to Entries closed: judging mode, judges per team, rubric and who judges which teams. In Automatic mode, judges are assigned now.",
  results: "Scoring and sign-up stop. Publish results when you are ready.",
};

export function backwardEffect(next: string): string {
  if (next === "draft") return "People who joined stay, but the join link stops working until you open it again.";
  if (next === "open" || next === "building") return "Entries open again. People who joined stay.";
  if (next === "closed") return "Entries reopen for teams with extra time. Other entries stay closed. People who joined stay. Scoring pauses and judging setup unlocks.";
  return "Judging opens again. Scores can change; publish again from Home afterwards.";
}

/** stageEffect is the line the stage picker shows for a move from current to target. */
export function stageEffect(current: string, target: string): string {
  const back = STAGE_ORDER.indexOf(target as never) < STAGE_ORDER.indexOf(current as never);
  return back ? backwardEffect(target) : FORWARD_EFFECT[target] || "";
}

/** stageChoices are the picker's options (every offered stage but the current one and archived) and the one chosen first (the next one forward). */
export function stageChoices(current: string, offered: string[] | undefined): { choices: string[]; initial: string } {
  const list = offered && offered.length ? offered : [...STAGE_ORDER];
  const order = STAGE_ORDER.filter((s) => list.includes(s) && s !== "archived");
  const cur = order.indexOf(current as never);
  const choices = order.filter((s) => s !== current);
  if (!choices.length) return { choices, initial: "" };
  const next = cur >= 0 && cur < order.length - 1 ? order[cur + 1] : choices[choices.length - 1];
  return { choices, initial: next };
}

export const isBack = (current: string, target: string) => STAGE_ORDER.indexOf(target as never) < STAGE_ORDER.indexOf(current as never);

const SETTINGS_RESULTS = "Settings → Results";

export function nextStepContent(ev: { stage: string; taken_down?: boolean }, step: Step | null | undefined, settingsHref: string, entriesHref: string): Content {
  if (!step || step.kind === "none")
    return {
      h2: ev.stage === "archived" ? "This event has ended" : ev.taken_down ? "This event is taken down" : "Read-only view",
      sentence: ev.stage === "archived" ? "The public page stays up as a record." : "",
      button: null,
    };
  if (step.kind === "entry_rules") return { h2: step.label || "", sentence: "Video or website is selected now. Change it if you want a code link too.", button: step.label || "", navHref: entriesHref };
  if (step.kind === "publish_results")
    return {
      h2: "Publish results",
      sentence: "",
      link: ["Teams see their results straight away. Your event page shows them the way you set in ", SETTINGS_RESULTS, settingsHref + "&tab=results", "."],
      confirmBody: "Teams see their results straight away. Your event page shows them the way you set in Settings → Results.",
      button: step.label || "Publish results",
    };
  const label = step.label || "";
  switch (step.stage) {
    case "open":
      return { h2: "Open sign-up when your page is ready", sentence: "People can join with your link, and judges can use their invite link.", button: label };
    case "building":
      return { h2: "Start building when people are in", sentence: "Entries open. Teams can edit their entry and website until you close entries.", button: label };
    case "closed":
      return step.deadline_passed
        ? { h2: "The entry deadline has passed", sentence: "Close entries, then set up judging. Teams with their own extension keep it.", button: label, warn: true }
        : { h2: "Close entries when building time is over", sentence: "Entries close for every team now, except teams with their own extension. Set up judging next.", button: label };
    case "judging":
      return { h2: "Start judging when every entry is in", sentence: "Judges can score, and the judging setup locks.", confirmBody: FORWARD_EFFECT.judging, button: label };
    case "results":
      return { h2: "Finish judging when every judge is done", sentence: "Scoring stops and judging locks. Next you publish results.", button: label };
    case "archived":
      if (step.blocked === "archive_needs_participants")
        return {
          h2: "End the event when you're done",
          sentence: "",
          link: ["Nobody is in this event, so it can't be ended. Delete it in ", "Settings", settingsHref, " instead."],
          button: label,
          blocked: true,
        };
      return { h2: "End the event when you're done", sentence: "Scoring and sign-up lock for good. Team sites are removed after 30 days.", button: label, isEnd: true };
    default:
      return { h2: label, sentence: "", button: label };
  }
}

/** checklistActionLabel is a checklist row's button: Read/Check/Score for the judge's rows, else the target's own word. */
export function checklistActionLabel(item: { id?: string; item_id?: string; target_id?: string }): string {
  const verb = ({ "j.rubric": "Read", "j.conflicts": "Check", "j.scores": "Score" } as Record<string, string>)[item.id || ""];
  if (verb) return verb;
  const t = (navigation.targets as readonly { id: string; action_label?: string | null }[]).find((x) => x.id === item.target_id);
  return t?.action_label || "Open";
}

/** targetHref is a navigation target's address for this event (nav.js resolve). */
export function targetHref(id: string, slug: string): string {
  const t = (navigation.targets as readonly { id: string; href_pattern?: string | null }[]).find((x) => x.id === id);
  return t?.href_pattern ? t.href_pattern.replace("{slug}", encodeURIComponent(slug)) : "/e/" + encodeURIComponent(slug) + "/manage?s=settings";
}

const sameDay = (a: string, b: string, ev: { time_zone?: string }) => dateTime(a, ev, { dateOnly: true }) === dateTime(b, ev, { dateOnly: true });
const timeOnly = (iso: string, ev: { time_zone?: string }) => {
  const full = dateTime(iso, ev);
  const i = full.lastIndexOf(", ");
  return i >= 0 ? full.slice(i + 2) : full;
};

/** cueHeading is the heading when a schedule row lines up with the next move ("Kickoff is now. Open sign-up?"). */
export function cueHeading(ev: { time_zone?: string }, step: Step | null | undefined, now = Date.now()): string {
  const c = step?.cue;
  if (!c || !step) return "";
  const label = MOVES[step.kind === "publish_results" ? "publish_results" : step.stage || ""] || step.label || "";
  const at = c.start_at && sameDay(c.start_at, new Date(now).toISOString(), ev) ? timeOnly(c.start_at, ev) : dateTime(c.start_at, ev);
  let phrase: string;
  switch (c.key) {
    case "entry_deadline":
      phrase = c.now ? "Entries are due now" : "Entries were due at " + at;
      break;
    case "event_start":
      phrase = c.now ? "The event starts now" : "The event started at " + at;
      break;
    case "event_end":
      phrase = c.now ? "The event ends now" : "The event ended at " + at;
      break;
    case "judging_end":
      phrase = c.now ? "Judging closes now" : "Judging closed at " + at;
      break;
    default:
      phrase = c.now ? c.title + " is now" : c.title + " was at " + at;
  }
  return phrase + ". " + label + "?";
}

/**
 * chooseStep is the one next step Home offers (organiser-overview.js): the
 * checklist's next, with publishing first at Results, and the lock row as Finish judging.
 */
export function chooseStep(d: OrgView, republish: boolean): Step {
  const ev = d.event;
  let step: Step = d.checklist ? { ...(d.checklist.next || { kind: "none" }) } : { ...(d.next_step || { kind: "none" }) };
  if (step.blocked_reason) step.blocked = step.blocked_reason;
  if (republish) step = { kind: "publish_results", label: "Publish again" };
  if (["o.publish", "o.ties"].includes(step.item_id || "") && step.target_id !== "o.lock") step = { ...step, kind: "publish_results", label: "Publish results" };
  if (!d.admin_view && !ev.taken_down && ev.stage === "results" && !ev.results_published && step.kind !== "publish_results") step = { kind: "publish_results", label: "Publish results" };
  if (step.kind === "link" && step.target_id === "o.lock") step = { ...step, kind: "stage", stage: "results" };
  return step;
}

/** content is the card for step, with the schedule cue folded in. */
export function cardContent(d: OrgView, step: Step, settingsHref: string, now = Date.now()): Content {
  const ev = d.event;
  const c = nextStepContent(ev, step, settingsHref, targetHref("o.entry-rules", ev.slug));
  if (step.kind === "link" && step.link) Object.assign(c, { h2: step.label || "", sentence: "", link: undefined, button: checklistActionLabel(step), navHref: step.link.href });
  const cue = c.button && !c.blocked ? cueHeading(ev, step, now) : "";
  if (cue) {
    if (step.kind === "link") c.cueNote = cueHeading(ev, d.next_step, now);
    else {
      c.h2 = cue;
      c.cueNote = "From your schedule. Nothing changes until you press " + c.button + ".";
    }
  }
  return c;
}

const teams = (k: number) => k + (k === 1 ? " team" : " teams");

/** judgingPoolLine: "3 teams will be judged. 1 team has no entry, so it won't be." */
export function judgingPoolLine(e: { judged?: number; no_entry?: number; excluded?: number }): string {
  const n = e.judged || 0;
  const none = e.no_entry || 0;
  const out = e.excluded || 0;
  let line = n ? teams(n) + " will be judged." : "No team has an entry to judge yet.";
  if (none) line += " " + teams(none) + (none === 1 ? " has no entry, so it won't be." : " have no entry, so they won't be.");
  if (out) line += " " + teams(out) + (out === 1 ? " you excluded stays out." : " you excluded stay out.");
  return line;
}

/** headsUp is the one warning the move's confirm shows (only one applies at a time). */
export function headsUp(step: Step, o: { pending: number; onNoTeam: number; judges: number; noTrack: number }): string {
  if (step.kind !== "stage") return "";
  if (step.stage === "building" && o.pending > 0) return o.pending + (o.pending === 1 ? " person is" : " people are") + " still waiting for approval. You can approve them later too.";
  if (step.stage === "closed" && o.onNoTeam > 0) return o.onNoTeam + (o.onNoTeam === 1 ? " person is" : " people are") + " not on a team yet.";
  if (step.stage === "judging" && o.judges === 0) return "No judges have joined yet. Share the judge link first.";
  if (step.stage === "judging" && o.noTrack > 0) return noTrackLine(o.noTrack);
  return "";
}

export const noTrackLine = (n: number) => n + " team" + (n === 1 ? "" : "s") + " with no track won't be judged until " + (n === 1 ? "it picks" : "they pick") + " one, or you assign one.";

export const END_CONFIRM = {
  title: "End this event?",
  body: "This is final. The event moves to Ended, scoring and sign-up lock for good, and team sites start their 30-day countdown before cleanup. Continue?",
  confirm: "End event",
};

// ---- the judging setup summary (before Start judging) ------------------------

export type JudgingSettings = { assignment_mode?: string; judges_per_team?: number; judges?: { name: string; organiser?: boolean }[] };
export type Coverage = { target: number; min: number; max: number; fix_href?: string; short: { name: string; have: number }[] };

/** setupLines are the summary's lines, each linking where it is changed; short is whether to say "Start anyway". */
export function setupLines(slug: string, s: JudgingSettings, cov: Coverage): { lines: [string, string][]; short: boolean } {
  const root = "/e/" + encodeURIComponent(slug) + "/manage?s=";
  const setup = root + "settings&tab=judging#";
  const grid = root + "assignments&grid=" + (s.assignment_mode === "panel" ? "panel" : "manual") + "#jg-grid-rows";
  const judges = s.judges || [];
  const automatic = s.assignment_mode === "automatic";
  const restricted = ["manual", "panel"].includes(s.assignment_mode || "");
  const range = cov.min === cov.max ? String(cov.min) : cov.min + "–" + cov.max;
  const perTeam = automatic ? "Judges per team: " + s.judges_per_team : restricted ? range + " per team" : "Every team can be scored by " + range + (cov.max === 1 ? " judge" : " judges");
  const mode = ({ open: "Open", automatic: "Automatic", manual: "Manual", panel: "Panel" } as Record<string, string>)[s.assignment_mode || ""] || s.assignment_mode || "";
  const lines: [string, string][] = [
    ["Mode: " + mode, setup + "mode"],
    [perTeam, restricted ? grid : setup + (automatic ? "jg-per" : "organiser-judges")],
    ["Who judges: " + (judges.map((j) => j.name + (j.organiser ? " (organiser)" : "")).join(", ") || "No judges"), setup + "organiser-judges"],
    [
      cov.short.length ? "Short: " + cov.short.map((t) => t.name + " (" + t.have + " of " + cov.target + ")").join(", ") : "Every team has at least " + cov.min + (cov.min === 1 ? " judge" : " judges"),
      cov.fix_href || setup + "organiser-judges",
    ],
  ];
  return { lines, short: cov.short.length > 0 || !judges.length };
}

// ---- results ---------------------------------------------------------------

export type Standing = { team_id: string; team_name?: string; rank?: number | null; total?: number | null; tied?: boolean; tie_decider?: string };

/** published reads GET E/results: its rows once published, else null. */
export function publishedRows(r: unknown): Standing[] | null {
  if (Array.isArray(r)) return r as Standing[];
  const rows = (r as { results?: Standing[] } | null)?.results;
  return Array.isArray(rows) ? rows : null;
}

/** scoresChangedSincePublish: some team's total in the published snapshot differs from the live standings. */
export function scoresChangedSincePublish(results: Standing[] | null, preview: Standing[], previewOk = true): boolean {
  if (!results || !previewOk) return false;
  const totals = (rows: Standing[]) => new Map((rows || []).map((row) => [row.team_id, row.total]));
  const pub = totals(results);
  const cur = totals(preview);
  return [...new Set([...pub.keys(), ...cur.keys()])].some((id) => pub.get(id) !== cur.get(id));
}

/** savedPlace is the final place the organiser gave a tied team last time, while the tie is still the same. */
export function savedPlace(row: Standing, group: Standing[], results: Standing[] | null): number | null {
  const saved = (results || []).find((r) => r.team_id === row.team_id);
  if (!saved || saved.tie_decider !== "organiser" || saved.total !== row.total) return null;
  const first = Number(row.rank);
  if ((saved.rank ?? 0) < first || (saved.rank ?? 0) >= first + group.length) return null;
  if (!group.every((r) => (results || []).some((old) => old.team_id === r.team_id && old.total === r.total && (old.rank ?? 0) >= first && (old.rank ?? 0) < first + group.length))) return null;
  return saved.rank ?? null;
}

/** ties are the tied groups of the live standings, by rank. */
export function ties(rows: Standing[]): [number, Standing[]][] {
  const by = new Map<number, Standing[]>();
  for (const r of rows) if (r.tied && r.rank != null) by.set(r.rank, [...(by.get(r.rank) || []), r]);
  return [...by.entries()].sort((a, b) => a[0] - b[0]);
}

export const rankText = (r: Standing) => (r.rank == null ? "Not scored" : "#" + r.rank);
export const scoreText = (r: Standing) => (r.total == null ? "No score" : String(r.total));

// ---- telling participants (SIM-341) ----------------------------------------

/** suggestedAnnouncement is the "Tell participants?" dialog's suggestion for a move, or null when there is none. */
export function suggestedAnnouncement(move: string, back = false): { title: string; body: string } | null {
  if (move === "publish_results") return { title: "Results are out", body: "Results are out. See them on the event page." };
  if (back) {
    switch (move) {
      case "draft":
        return { title: "Sign-up is paused", body: "Sign-up is paused for now. Everyone who joined stays in." };
      case "open":
      case "building":
        return { title: "Entries are open again", body: "Entries are open again. You can edit your entry and website." };
      case "closed":
        return { title: "Entries are closed", body: "Entries are closed. Teams with extra time can still edit." };
      case "judging":
        return { title: "Judging has reopened", body: "Judging has reopened. Entries stay as they are." };
    }
    return null;
  }
  switch (move) {
    case "open":
      return { title: "Sign-up is open", body: "Sign-up is open. Join with the event link and find a team." };
    case "building":
      return { title: "Building has started", body: "Building has started. Add your entry and website before entries close." };
    case "closed":
      return { title: "Entries are closed", body: "Entries are closed. Thanks for building!" };
    case "judging":
      return { title: "Judging has started", body: "Judging has started. Entries, teams and websites can no longer be changed." };
    case "results":
      return { title: "Judging is done", body: "Judging is done. Results are coming soon." };
    case "archived":
      return { title: "The event has ended", body: "The event has ended. Thanks for taking part. The event page stays up as a record." };
  }
  return null;
}

/** postedLine is what the toast says after an announcement posts (the same words as Announcements). */
export function postedLine(emailed: boolean, r: { emails_queued?: number; organiser_copied?: boolean }): { text: string; bad?: boolean } {
  if (!emailed) return { text: "Posted" };
  const queued = r.emails_queued || 0;
  if (queued === 0) return { text: "Posted. Nobody had an email address on file, so no email was sent.", bad: true };
  return { text: `Posted and emailed to ${queued} ${queued === 1 ? "person" : "people"}${r.organiser_copied ? " (including a copy to you)" : ""}.` };
}

// ---- the checklist ---------------------------------------------------------

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

/** itemDetail is the line under a checklist row: Optional, Not needed, the server's detail or why it is blocked. */
export function itemDetail(item: ChecklistItem): string {
  const done = item.state === "done";
  const opted = item.state === "not_needed";
  return [item.optional && !done && !opted && item.id !== "j.closes" ? "Optional" : "", opted ? "Not needed" : item.detail || (!done && item.blocked_reason ? BLOCKED[item.blocked_reason] || "" : "")]
    .filter(Boolean)
    .join(" · ");
}

export const CHECKLIST_GROUPS: [string, string][] = [
  ["earlier_required", "Earlier required"],
  ["current", ""],
  ["earlier_optional", "Earlier items"],
  ["history", "History"],
];
