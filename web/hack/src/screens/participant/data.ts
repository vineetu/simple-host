// The participant's reading screens in plain logic (the old
// participant-home.js, schedule.js, needs.js and participant-results.js):
// what the next-step card says, the four rows of "Your entry", the schedule's
// Now/Next and the Home nudge, and how a result reads. No React here.
import type { EventDetail, Need } from "@/lib/client";
import { dateTime, nextLocalMidnight } from "@/lib/time";
import { href } from "@/routes/resolve";

// ---- Payloads these screens read (GET E's extra fields, and the area calls that are still Untyped) ----

export type Step = {
  id: "team" | "summary" | "evidence" | "submit";
  done?: boolean;
  detail?: string;
  filled?: number;
  of?: number;
  missing?: string[];
  summary?: string;
  status?: "draft" | "submitted" | "not_submitted" | string;
  submitted_at?: string | null;
  auto?: boolean;
};
export type Progress = {
  steps: Step[];
  next: "" | "team" | "summary" | "evidence" | "submit" | string;
  title?: string;
  deadline?: string | null;
  deadline_is_extension?: boolean;
  event_deadline?: string | null;
  server_now?: string | null;
};
export type ChecklistItem = { id: string; label?: string; state?: string; link?: { href?: string } | null; target_id?: string };
export type HomeTeam = {
  slug: string;
  name?: string;
  admin?: boolean;
  pending_requests?: number;
  deadline?: string | null;
  frozen?: boolean;
  site?: { exists?: boolean; url?: string } | null;
};
/** GET E with the fields Home reads. */
export type HomeDetail = EventDetail & {
  event: EventDetail["event"] & { url?: string };
  me?: EventDetail["me"] & { team?: HomeTeam | null; team_removal_notice?: string };
  progress?: Progress | null;
  checklist?: { items?: ChecklistItem[] } | null;
};
export type OpenTeams = { enabled?: boolean; teams?: unknown[]; pending?: { kind?: string; team_name?: string }[] };
export type Announcement = { id: string; title: string; body_html?: string; created_at: string; pinned?: boolean };
export type ScheduleRow = { key?: string; title: string; description?: string; start_at: string; end_at?: string | null; all_day?: boolean; fixed?: boolean };
export type Content = { schedule?: ScheduleRow[]; fixed_schedule?: ScheduleRow[] };
export type Standing = { rank?: number | null; team_name: string; raw_total?: number | null; normalised_total?: number | null; deciding_mode?: string };
export type MyResults = Standing & {
  published?: boolean;
  tied?: boolean;
  track_winner?: boolean;
  track?: { name?: string; prize?: string } | null;
  comments?: string[];
  ranked_teams?: number;
  shown_publicly?: boolean;
  entry?: {
    title?: string;
    track?: { name?: string } | null;
    problem?: string;
    tagline?: string;
    audience?: string;
    tools?: string[];
    maturity?: "working" | "partial" | "concept" | "";
    site_url?: string;
    video_url?: string;
    video_host?: string;
    code_url?: string;
    code_host?: string;
  } | null;
};
export type Standings = { winners?: Standing[]; ranking?: Standing[] };

// ---- Words ----

const PLAIN: Record<string, string> = {
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
};
export const plain = (f: string) => PLAIN[f] || f;

export function joinAnd(items: string[]): string {
  if (!items.length) return "";
  if (items.length === 1) return items[0];
  return items.slice(0, -1).join(", ") + " and " + items[items.length - 1];
}

export const stepOf = (p: Progress | null | undefined, id: Step["id"]): Step => (p?.steps || []).find((s) => s.id === id) || ({ id } as Step);

const ms = (iso: string | null | undefined) => new Date(iso || "").getTime();

/** entriesOpen: no deadline means entries stay open until the stage moves on. */
export function entriesOpen(stage: string, p: Progress): boolean {
  if (["judging", "results", "archived"].includes(stage)) return false;
  return !p.deadline || (p.server_now ? ms(p.server_now) : Date.now()) < ms(p.deadline);
}

/** deadlineText: "Entries close …", "Your team closes …, 3 hours after everyone else", "Entries closed …". */
export function deadlineText(ev: EventDetail["event"], p: Progress): string {
  if (!p.deadline) return "";
  const dt = dateTime(p.deadline, ev);
  const now = p.server_now ? ms(p.server_now) : Date.now();
  if (now >= ms(p.deadline)) return "Entries closed " + dt;
  if (p.deadline_is_extension && p.event_deadline) {
    const h = Math.round((ms(p.deadline) - ms(p.event_deadline)) / 3600000);
    return "Your team closes " + dt + (h > 0 ? ", " + h + " hour" + (h === 1 ? "" : "s") + " after everyone else" : "");
  }
  return "Entries close " + dt;
}

/** countdownText is the old ui.countdown's words for the time left. */
export function countdownText(left: number): string {
  if (left <= 0) return "0 s";
  let s = Math.round(left / 1000);
  const d = Math.floor(s / 86400);
  s -= d * 86400;
  const h = Math.floor(s / 3600);
  s -= h * 3600;
  const m = Math.floor(s / 60);
  s -= m * 60;
  if (d >= 2) return d + " days";
  if (d === 1) return "1 d " + h + " h " + m + " m";
  if (h > 0) return h + " h " + m + " m";
  if (m > 0) return m + " m " + String(s).padStart(2, "0") + " s";
  return s + " s";
}

// ---- The next-step card ----

export type Hero = { title: string; note?: string; label?: string; href?: string; external?: boolean; kind?: string };

/** hero is "Your next step", first matching state wins (participant-home.js render). */
export function hero(d: HomeDetail, finding: OpenTeams | null, now = Date.now()): Hero {
  const ev = d.event;
  const me = d.me || {};
  const team = me.team || null;
  const slug = ev.slug;
  const teamHref = href("participant", slug, "team");
  const entryHref = href("participant", slug, "entry");
  const teamsOpen = (ev.stage === "open" || ev.stage === "building") && !ev.taken_down;
  const deadlinePassed = !!(ev.submission_deadline && ms(ev.submission_deadline) <= now);
  const deadline = ev.submission_deadline ? dateTime(ev.submission_deadline, ev) : "";
  const teamDeadline = team?.deadline ? dateTime(team.deadline, ev) : deadline;
  const teamFrozen = team ? !!team.frozen : deadlinePassed;
  const site = team?.site?.exists && team.site.url ? team.site.url : "";
  const yourSite = (): Pick<Hero, "label" | "href" | "external"> => (site ? { label: "View your site", href: site, external: true } : { label: "Go to your team", href: teamHref });

  if (ev.results_published)
    return { title: ev.stage === "archived" ? "This event has ended. Results are out." : "Results are out", label: "See results", href: href("participant", slug, "results") };
  if (ev.stage === "archived") return { title: "This event has ended", note: "The public page stays up as a record.", label: "Open public page", href: ev.url, external: true };
  if (!team && me.team_removal_notice)
    return { title: me.team_removal_notice, ...(teamsOpen && !deadlinePassed ? { label: "Find a team", href: teamHref } : {}) };
  if (!team) {
    const invite = (finding?.pending || []).find((p) => p.kind === "invite");
    if (invite) return { kind: "invite", title: "You have an invite from " + invite.team_name, note: deadline ? "Entries close " + deadline + "." : "", label: "Go to Team", href: teamHref };
    if (teamsOpen && !deadlinePassed) {
      const n = Array.isArray(finding?.teams) ? finding!.teams!.length : 0;
      const open = n ? n + " team" + (n === 1 ? " is" : "s are") + " looking for members." : "";
      return { title: "Find a team to start building", note: [open, deadline ? "Entries close " + deadline + "." : ""].filter(Boolean).join(" "), label: "Find a team", href: teamHref };
    }
    return { title: "You're not on a team", note: deadline ? "Entries closed " + deadline + "." : "" };
  }
  if (ev.stage === "judging" || ev.stage === "results")
    return { title: ev.stage === "results" ? "Judging is done. Results are coming soon" : "Judges are scoring now. Results come next", ...yourSite() };
  if (teamFrozen) return { title: "Entries are closed. Judging starts soon", ...yourSite() };
  if (team.admin && (team.pending_requests || 0) > 0) {
    const n = team.pending_requests!;
    return {
      kind: "join_request",
      title: n + (n === 1 ? " person" : " people") + " asked to join " + team.name,
      note: "Say yes or no on your Team page.",
      label: n === 1 ? "See request" : "See requests",
      href: teamHref + "&tab=members",
    };
  }
  const p = d.progress;
  if (!p) return { title: teamDeadline ? "Keep building. Entries close " + teamDeadline : "You are on " + team.name + ". Keep building", label: "Go to your entry", href: entryHref };
  if (stepOf(p, "submit").status === "not_submitted")
    return { title: team.name + " is not complete", note: "Not complete. It will still be judged, marked incomplete.", label: "Go to your entry", href: entryHref };
  if (p.next === "summary") {
    const words = (stepOf(p, "summary").missing || []).map(plain);
    return {
      title: "Finish your summary card" + (words.length > 2 ? ": " + words.length + " lines left" : words.length ? ": add " + joinAnd(words) : ""),
      label: "Finish your entry",
      href: entryHref,
    };
  }
  if (p.next === "evidence") {
    const s = stepOf(p, "evidence").summary;
    return { title: s ? "Add " + s : "Show your work", label: "Publish your website", href: href("participant", slug, "website") };
  }
  if (p.next === "submit") return { title: (p.title || team.name) + " is ready. Submit it so judges can see it.", label: "Go to your entry", href: entryHref };
  return {
    title: (p.title || team.name) + " is submitted",
    note: teamDeadline ? "You can still change it until " + teamDeadline + ". It stays submitted." : "It stays submitted.",
    label: "View your entry",
    href: entryHref,
  };
}

// ---- "Your entry" ----

export type EntryRow = {
  id: string;
  label: string;
  detail: string;
  done: boolean;
  /** Settled without being done (not submitted at the deadline): shown quiet. */
  settled: boolean;
  current: boolean;
  action?: { label: string; href: string } | "submit";
};
export type EntrySection = { rows: EntryRow[]; deadline: string; countdown: { to: string; left: string; quiet: boolean } | null; serverNow?: string | null };

/** noTeamProgress: before a team there is no server progress, but the whole path shows from minute one. */
export function noTeamProgress(d: HomeDetail): Progress | null {
  const ev = d.event;
  if (d.me?.team || !["open", "building"].includes(ev.stage) || ev.taken_down) return null;
  return {
    steps: [{ id: "team" }, { id: "summary" }, { id: "evidence" }, { id: "submit", status: "draft" }],
    next: "team",
    deadline: ev.submission_deadline || null,
    server_now: null,
  };
}

/** entrySection is the Your entry card, or null when it has nothing to say (judging on). */
export function entrySection(d: HomeDetail): EntrySection | null {
  const ev = d.event;
  if (ev.results_published || ["judging", "results", "archived"].includes(ev.stage)) return null;
  const p = d.progress || noTeamProgress(d);
  if (!p) return null;
  const slug = ev.slug;
  const items = d.checklist?.items || [];
  const item = (id: string) => items.find((i) => i.id === id);
  const open = entriesOpen(ev.stage, p);
  const team = stepOf(p, "team"),
    summary = stepOf(p, "summary"),
    evidence = stepOf(p, "evidence"),
    submit = stepOf(p, "submit");
  const nowId = p.next === "submit" ? "" : "p." + p.next;
  const row = (id: string, step: { done?: boolean; opt?: boolean }, label: string, detail: string, action?: EntryRow["action"]): EntryRow => {
    const done = item(id)?.state ? item(id)!.state === "done" : !!step.done;
    return { id, label, detail, done, settled: !!step.opt, current: !done && nowId === id, action };
  };
  const entryHref = href("participant", slug, "entry");
  const summarySub = !team.done
    ? "Problem, solution, who it's for, track, tools and what works."
    : summary.done
      ? "All " + (summary.of || 0) + " lines filled."
      : (summary.filled || 0) + " of " + (summary.of || 0) + " filled" + ((summary.missing || []).length ? ": add " + joinAnd(summary.missing!.map(plain)) : "") + ".";
  const evItem = item("p.evidence");
  const evidenceSub = !team.done
    ? "A video or your website, as the event asks."
    : evidence.done
      ? "Done."
      : evidence.summary && evidence.summary.includes(" or ") && !evidence.summary.includes(", and ")
        ? "Nothing yet. One of these is enough."
        : "Nothing yet.";
  let submitSub: string;
  let submitAction: EntryRow["action"];
  if (submit.status === "not_submitted") submitSub = "Not complete. It will still be judged, marked incomplete.";
  else if (submit.status === "submitted") submitSub = submit.auto ? "Submitted automatically at the deadline." : "Submitted" + (submit.submitted_at ? " " + dateTime(submit.submitted_at, ev) : "") + ".";
  else if (!team.done) submitSub = "Then the judges can see it. You can still change it after.";
  else if (!open)
    submitSub = p.next === "submit" ? "Entries are closed. It is being submitted for you automatically." : "Entries are closed. Not complete. It will still be judged, marked incomplete.";
  else if (p.next === "submit") {
    submitSub = "Ready. Then the judges can see it. You can still change it after.";
    submitAction = "submit";
  } else submitSub = "Then the judges can see it. You can still change it after.";
  const settled = submit.status === "submitted" || submit.status === "not_submitted";
  const rows = [
    row("p.team", team, "Join or start a team", team.detail || "You're not on a team yet.", team.done ? undefined : { label: "Find a team", href: href("participant", slug, "team") }),
    row("p.summary", summary, "Fill in the summary card", summarySub, !summary.done && team.done && open ? { label: "Edit", href: entryHref } : undefined),
    row(
      "p.evidence",
      evidence,
      evidence.summary ? "Add " + evidence.summary : "Show your work",
      evidenceSub,
      !evidence.done && team.done && open ? { label: evItem?.target_id === "p.website" ? "Publish website" : "Open Entry", href: evItem?.link?.href || entryHref } : undefined,
    ),
    row("p.submit", { done: submit.status === "submitted", opt: submit.status === "not_submitted" }, "Submit before the deadline", submitSub, submitAction),
  ];
  const countdown = open && p.deadline && (!settled || submit.status === "submitted") ? { to: p.deadline, left: submit.status === "submitted" ? "left to make changes" : "left", quiet: submit.status === "submitted" } : null;
  return { rows, deadline: deadlineText(ev, p), countdown, serverNow: p.server_now };
}

/** needsFor is the Needs you list without what the next-step card already leads with. */
export const needsFor = (needs: Need[] | undefined, heroKind?: string) => (needs || []).filter((n) => n.kind !== heroKind);

const OPEN: Record<string, string> = { team: "Open Team", website: "Open Website", entry: "Open Entry", people: "Review applications", home: "", queue: "Open My teams" };
/** splitNeed: the server's first sentence as the row's title, the rest (or where it goes) under it. */
export function splitNeed(item: Need): [string, string] {
  const text = String(item.text || "");
  if (item.kind === "team_message") return [text, OPEN[item.section || ""] || ""];
  const i = text.indexOf(". ");
  if (i > 0) return [text.slice(0, i + 1), text.slice(i + 2)];
  return [text, OPEN[item.section || ""] || ""];
}

/** justPlaced: on a team now, after this browser last saw this person with none (shared with Team's own note). */
export function justPlaced(uid: string, slug: string, teamSlug: string): boolean {
  const key = "sh-team-baseline-" + uid + "-" + slug;
  let baseline: string | null = null;
  try {
    baseline = localStorage.getItem(key);
    localStorage.setItem(key, teamSlug);
  } catch {
    /* storage blocked */
  }
  if (baseline === null) return false;
  return baseline !== teamSlug && !!teamSlug;
}

/** markAnnouncementsSeen: the newest one this browser has shown, so the rail's unread count clears. */
export function markAnnouncementsSeen(slug: string, items: Announcement[]) {
  if (!items.length) return;
  const newest = items.reduce((n, a) => (ms(a.created_at) > ms(n) ? a.created_at : n), items[0].created_at);
  try {
    localStorage.setItem("sh_ann_seen:" + slug, newest);
  } catch {
    /* storage blocked */
  }
}

// ---- Schedule ----

export type Row = ScheduleRow & { now?: boolean; next?: boolean };
type Zoned = { time_zone?: string };
const valid = (iso?: string | null) => !!iso && !Number.isNaN(ms(iso));

function sortAt(r: Row, ev: Zoned): number {
  const t = ms(r.start_at);
  if (!(r.all_day && r.key === "event_end")) return t;
  const next = nextLocalMidnight(r.start_at, ev);
  return next != null ? next - 1000 : t + 86399000;
}

/** merge is the organisers' rows and the event's real times in one list, in time order. */
export function merge(c: Content | null | undefined, ev: Zoned): Row[] {
  const rows: Row[] = [];
  for (const it of c?.schedule || []) if (valid(it.start_at)) rows.push({ ...it, fixed: false, all_day: false });
  for (const it of c?.fixed_schedule || []) if (valid(it.start_at)) rows.push({ ...it, fixed: true });
  return rows.sort((a, b) => sortAt(a, ev) - sortAt(b, ev));
}

/** label marks Now (a window containing this moment) and Next (the first row still to start). */
export function label(rows: Row[], now = Date.now()): Row[] {
  let nowIdx = -1,
    nextIdx = -1;
  rows.forEach((r, i) => {
    if (r.all_day) return;
    const s = ms(r.start_at);
    const e = valid(r.end_at) ? ms(r.end_at) : null;
    if (nowIdx < 0 && e !== null && s <= now && now < e) nowIdx = i;
    if (nextIdx < 0 && s > now) nextIdx = i;
  });
  return rows.map((r, i) => ({ ...r, now: i === nowIdx, next: i === nextIdx }));
}

const SOON = 15 * 60 * 1000;
export function nudge(rows: Row[], now = Date.now()): { soon: Row | null; on: Row | null } {
  let soon: Row | null = null,
    on: Row | null = null;
  for (const r of rows) {
    if (r.all_day) continue;
    const s = ms(r.start_at);
    const e = valid(r.end_at) ? ms(r.end_at) : null;
    if (s > now && s - now <= SOON && (!soon || s < ms(soon.start_at))) soon = r;
    if (e !== null && s <= now && now < e && (!on || s > ms(on.start_at))) on = r;
  }
  return { soon, on };
}

const minutes = (n: number) => n + (n === 1 ? " minute" : " minutes");
export function soonText(r: Row, now = Date.now()): string {
  const m = minutes(Math.max(1, Math.ceil((ms(r.start_at) - now) / 60000)));
  switch (r.key) {
    case "entry_deadline":
    case "team_deadline":
      return "Entries are due in " + m;
    case "event_start":
      return "The event starts in " + m;
    case "event_end":
      return "The event ends in " + m;
    case "judging_end":
      return "Judging closes in " + m;
    default:
      return r.title + " starts in " + m;
  }
}

/** timeOnly is the time half of dateTime ("6:00 pm"). */
export function timeOnly(iso: string, ev: Zoned): string {
  const full = dateTime(iso, ev);
  const i = full.lastIndexOf(", ");
  return i >= 0 ? full.slice(i + 2) : full;
}
const sameDay = (a: string, b: string, ev: Zoned) => dateTime(a, ev, { dateOnly: true }) === dateTime(b, ev, { dateOnly: true });

/** nudgeLine is the one line above Home's next step and Schedule's list: [bold part, plain part], or null. */
export function nudgeLine(rows: Row[], ev: Zoned, now = Date.now()): [string, string] | null {
  const { soon, on } = nudge(rows, now);
  if (!soon && !on) return null;
  if (soon) return [soonText(soon, now) + ".", on ? on.title + " is on now." : ""];
  const until = sameDay(new Date(now).toISOString(), on!.end_at!, ev) ? timeOnly(on!.end_at!, ev) : dateTime(on!.end_at, ev);
  return [on!.title + " is on now.", "Until " + until + "."];
}

/** whenText is a schedule row's time: a date, a moment, or a window. */
export function whenText(r: Row, ev: Zoned): string {
  if (r.all_day) return dateTime(r.start_at, ev, { dateOnly: true });
  if (!r.end_at) return dateTime(r.start_at, ev);
  return dateTime(r.start_at, ev) + " – " + (sameDay(r.start_at, r.end_at, ev) ? timeOnly(r.end_at, ev) : dateTime(r.end_at, ev));
}

// ---- Results ----

/** scoreText: two decimals at most, trailing zeros trimmed, never rounded below what it has (the public page's). */
export function scoreText(n: number | null | undefined): string {
  if (typeof n !== "number" || Number.isNaN(n)) return "";
  return n.toFixed(2).replace(/0+$/, "").replace(/\.$/, "");
}
export function ordinal(n: number): string {
  const v = n % 100;
  if (v >= 11 && v <= 13) return n + "th";
  return n + ({ 1: "st", 2: "nd", 3: "rd" }[n % 10] || "th");
}
export const decidingScore = (r: Standing | null | undefined) => (r ? (r.deciding_mode === "normalised" ? r.normalised_total : r.raw_total) : null);

/** headline is the team's place: "Joint 2nd of 5 judged teams", "1st", or "Not scored yet". */
export function headline(mine: MyResults, ranked: number | null): string {
  if (mine.rank == null) return "Not scored yet";
  return (mine.tied ? "Joint " : "") + ordinal(mine.rank) + (ranked ? " of " + ranked + (ranked === 1 ? " judged team" : " judged teams") : "");
}
