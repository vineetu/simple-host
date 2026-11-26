// The Entry screen's plain logic (participant-entry.js): the event's rule for
// each line (Required / Optional / Off / One of), the status block's words,
// the missing-items list, what changed since the entry was read, and the
// lines a teammate's edit differs on.
import type { EntryCard } from "@/lib/client";
import { ucfirst } from "../team/words";

export type Mode = "required" | "optional" | "off" | "one_of";
export type Rules = { fields?: Record<string, Mode>; summary?: string; preset?: string; judge_first?: string; has_tracks?: boolean };
export type Entry = EntryCard & { description?: string; updated_at?: string; track?: { slug?: string; name?: string } | null };
/** GET/PUT E/entry and the submit/unsubmit answers. */
export type EntryData = {
  entry?: Entry;
  rules?: Rules;
  editable?: boolean;
  status?: "draft" | "submitted" | "not_submitted";
  missing?: string[];
  complete?: boolean;
  progress?: { steps?: { id: string; filled?: number; of?: number }[] };
  submitted_at?: string | null;
  submitted_by?: string;
  submit_auto?: boolean;
  site?: { exists?: boolean; live_version?: number | string | null };
  refusal?: string;
};
export type Criterion = { name: string; description?: string; max_points?: number };

export const SUMMARY = ["problem", "tagline", "audience", "track", "tools", "maturity"];
export const SHOW = ["video_url", "site", "code_url", "screenshot"];
export const LABEL: Record<string, string> = {
  title: "Project name",
  problem: "Problem, in one line",
  tagline: "Solution, in one line",
  audience: "Who it's for",
  track: "Track",
  tools: "Tools and AI you used",
  maturity: "What works today",
  video_url: "Video link",
  site: "Website",
  code_url: "Code link",
  screenshot: "Screenshot",
  description: "Anything else judges should know",
};
const PLAIN: Record<string, string> = {
  title: "project name",
  problem: "the problem, in one line",
  tagline: "the solution, in one line",
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
const NAME: Record<string, string> = {
  title: "Project name",
  problem: "Problem",
  tagline: "Solution",
  audience: "Who it's for",
  track: "Track",
  tools: "Tools and AI",
  maturity: "What works today",
  description: "Description",
  video_url: "Video link",
  site: "Website",
  code_url: "Code link",
  screenshot: "Screenshot",
};
export const MAXLEN: Record<string, number> = { title: 80, problem: 140, tagline: 160, audience: 100, description: 5000 };
export const HELP: Record<string, string> = {
  problem: "What goes wrong today, and for whom.",
  tagline: "What you built. This line also shows on the team sites list.",
  video_url: "YouTube, Vimeo, Loom or Google Drive play right on the judges’ screen. Other links open in a new tab.",
};
/** ADD is the line under a field that a refused submit names. */
export const ADD: Record<string, string> = {
  title: "Add a project name.",
  problem: "Add the problem, in one line.",
  tagline: "Add the solution, in one line.",
  audience: "Add who it's for.",
  track: "Choose a track.",
  tools: "Add at least one tool.",
  maturity: "Choose what works today.",
  description: "Add a description.",
  video_url: "Add a video link.",
  code_url: "Add a code link.",
  screenshot: "Add a screenshot.",
};
export const CLOSED: Record<string, string> = {
  submissions_closed: "Entries are closed",
  team_site_taken_down: "The organiser has taken your team’s site down.",
  event_taken_down: "This event has been taken down.",
  event_closed: "This event has ended.",
};

/** mode is the event's rule for one line: a missing key is Optional; the project name is always Required. */
export function mode(rules: Rules | undefined, hasTracks: boolean, field: string): Mode {
  if (field === "title") return "required";
  if (field === "track" && !hasTracks) return "off";
  return rules?.fields?.[field] || "optional";
}

const isEvidence = (f: string) => f === "video_url" || f === "site" || f === "code_url" || f === "screenshot" || f === "one_of";
const isSummary = (f: string) => SUMMARY.includes(f) || f === "title" || f === "description";

/** need is the status block's two lines: the summary card's and show-your-work's, each ticked when met. */
export function need(data: EntryData): { text: string; ok: boolean }[] {
  const missing = data.missing || [];
  const rules = data.rules || {};
  const label = rules.summary || "show your work";
  const step = (data.progress?.steps || []).find((s) => s.id === "summary");
  const sumMissing = missing.filter(isSummary);
  const evMissing = missing.filter((f) => !isSummary(f));
  const summary =
    "Summary card" + (step?.of ? " · " + (step.filled || 0) + " of " + step.of : "") + (sumMissing.length ? " · Missing: " + sumMissing.map((f) => NAME[f] || f).join(", ") : "");
  const extra = evMissing.filter((f) => f !== "one_of" && !(f === "code_url" && /code link/.test(label)));
  const out = [{ text: summary, ok: !missing.some(isSummary) }];
  if (rules.summary || evMissing.length) out.push({ text: ucfirst(label) + (extra.length ? " · Missing: " + extra.map((f) => NAME[f] || f).join(", ") : ""), ok: !missing.some(isEvidence) });
  return out;
}

/** tightened is the sentence for a submitted entry the rules now ask more of. */
export const tightened = (missing: string[]) => "The organisers now also ask for " + missing.map((f) => PLAIN[f] || f).join(", ") + ". Your entry stays submitted. Add it if you can.";

/** hasAnyContent: anything at all in the saved entry (a team that never started has nothing). */
export const hasAnyContent = (e: Entry) =>
  !!(e.title || e.problem || e.tagline || e.audience || e.maturity || (e.tools && e.tools.length) || e.video_url || e.code_url || e.has_screenshot);

export type Values = { title: string; problem: string; tagline: string; audience: string; video_url: string; code_url: string; description: string; track: string; maturity: string; tools: string[] };

export const valuesOf = (e: Entry): Values => ({
  title: e.title || "",
  problem: e.problem || "",
  tagline: e.tagline || "",
  audience: e.audience || "",
  video_url: e.video_url || "",
  code_url: e.code_url || "",
  description: e.description || "",
  track: e.track?.slug || "",
  maturity: e.maturity || "",
  tools: Array.isArray(e.tools) ? e.tools.slice() : [],
});

/** diff is the PUT body: only the lines that changed (links trimmed), as the old diffBody. */
export function diff(e: Entry, v: Values, shown: { track: boolean }): Record<string, unknown> {
  const was = valuesOf(e);
  const body: Record<string, unknown> = {};
  for (const f of ["title", "problem", "tagline", "audience", "description"] as const) if (v[f] !== was[f]) body[f] = v[f];
  for (const f of ["video_url", "code_url"] as const) if (v[f].trim() !== was[f]) body[f] = v[f].trim();
  if (shown.track && v.track !== was.track) body.track = v.track;
  if (v.maturity !== was.maturity) body.maturity = v.maturity;
  if (JSON.stringify(v.tools) !== JSON.stringify(was.tools)) body.tools = v.tools;
  return body;
}

/** addTool adds one tag (trimmed, 30 characters, no repeats, up to 10). */
export function addTool(tools: string[], raw: string): string[] {
  const v = String(raw || "").trim();
  if (!v || tools.length >= 10 || tools.some((t) => t.toLowerCase() === v.toLowerCase())) return tools;
  return [...tools, v.slice(0, 30)];
}

const CONFLICT = ["title", "problem", "tagline", "audience", "video_url", "code_url", "description", "maturity"] as const;
const CONFLICT_LABEL: Record<string, string> = {
  title: "title",
  problem: "problem",
  tagline: "solution",
  audience: "who it's for",
  video_url: "video link",
  code_url: "code link",
  description: "description",
  maturity: "what works today",
};
/** conflicts are the lines where this person's edit and the teammate's saved entry differ. */
export function conflicts(mine: Record<string, unknown>, entry: Entry, newer: Entry): { label: string; mine: string; theirs: string }[] {
  const of = (f: string) => String((f in mine ? mine[f] : (entry as Record<string, unknown>)[f]) || "");
  return CONFLICT.filter((f) => of(f) !== String((newer as Record<string, unknown>)[f] || "")).map((f) => ({
    label: CONFLICT_LABEL[f],
    mine: of(f),
    theirs: String((newer as Record<string, unknown>)[f] || ""),
  }));
}

/** fieldOf is the line a save refusal belongs to, by its code. */
export function fieldOf(code: string, sent: Record<string, unknown>): string[] {
  if (code === "invalid_url") return ["video_url", "code_url"].filter((f) => f in sent);
  if (code === "invalid_track") return ["track"];
  if (code === "image_too_large" || code === "unsupported_image") return ["screenshot"];
  const f = ["title", "problem", "tagline", "audience", "tools", "maturity", "description"].find((x) => code === "invalid_" + x);
  return f ? [f] : [];
}

/** marks are the error lines a refused submit puts under each missing line ("one_of" and "site" sit on the show-your-work group). */
export function marks(missing: string[], rules: Rules): Record<string, string> {
  const out: Record<string, string> = {};
  for (const f of missing) {
    if (f === "one_of") out.one_of = "Add " + (rules.summary || "a video or a website") + ".";
    else if (f === "site") out.one_of = "Publish your website.";
    else if (ADD[f]) out[f] = ADD[f];
  }
  return out;
}
