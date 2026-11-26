// Entries and Entry detail: the payloads they read, and their plain words
// (organiser-entries.js, organiser-entry-detail.js, _orgB-util.js).
import type { EntryCard } from "@/lib/client";

/** One row of GET E/entries. */
export type EntryItem = EntryCard & {
  team: { slug: string; name: string };
  status?: "draft" | "submitted" | "not_submitted" | "";
  submitted_at?: string | null;
  missing?: string[];
  incomplete?: boolean;
  excluded?: boolean;
  site_exists?: boolean;
  site_taken_down?: boolean;
  description?: string;
};
export type EntryCounts = { submitted?: number; drafts?: number; not_started?: number; not_submitted?: number; sites_live?: number };
export type EntriesList = { entries?: EntryItem[]; counts?: EntryCounts };

/** One team of GET E/teams, as these screens read it. */
export type RunTeam = {
  slug: string;
  name: string;
  extended?: boolean;
  deadline_override?: string | null;
  deadline?: string | null;
  members?: { user_id?: string; display_name?: string }[];
  site?: { exists?: boolean; taken_down?: boolean; taken_down_reason?: string; url?: string } | null;
};
export type TeamsList = { teams?: RunTeam[] };
export type Usage = { saved_data_bytes?: number; history_bytes?: number; screenshot_bytes?: number; file_bytes?: number };
export type EntryRules = { fields?: Record<string, string> };

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

/** labelList names the missing fields in words. */
export const labelList = (fields: string[] | undefined) => (fields || []).map((f) => FIELD_LABEL[f] || f);

/** joinAnd: "a", "a and b", "a, b and c". */
export function joinAnd(items: string[]): string {
  if (!items.length) return "";
  if (items.length === 1) return items[0];
  return items.slice(0, -1).join(", ") + " and " + items[items.length - 1];
}

/** fmtBytes: 512 B, 1.7 MB, 12 MB. */
export function fmtBytes(n: unknown): string {
  const b = Number(n) || 0;
  if (b < 1024) return b + " B";
  const units = ["KB", "MB", "GB", "TB"];
  let v = b / 1024;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return (v >= 10 ? Math.round(v) : Math.round(v * 10) / 10) + " " + units[i];
}

export const usageTotal = (u: Usage) => (u.saved_data_bytes || 0) + (u.history_bytes || 0) + (u.screenshot_bytes || 0) + (u.file_bytes || 0);

/** hasAnyContent: no field says "never touched", so it is every line the form fills being empty. */
export function hasAnyContent(e: EntryItem): boolean {
  return !!(e.title || e.problem || e.tagline || e.audience || e.maturity || (e.tools && e.tools.length) || e.video_url || e.code_url || e.has_screenshot);
}

export type Tone = "neutral" | "ok" | "down" | "accent";

/** status is the row's word and its badge tone. */
export function status(e: EntryItem): { label: string; tone: Tone } {
  if (e.excluded) return { label: "Excluded", tone: "down" };
  if (e.status === "submitted") return { label: "Submitted", tone: "ok" };
  if (e.incomplete) return { label: "Incomplete", tone: "accent" };
  if (!hasAnyContent(e)) return { label: "Not started", tone: "neutral" };
  return { label: "Draft", tone: "accent" };
}

/** progress is the row's first line: submitted when, what is missing, or nothing yet. */
export function progress(e: EntryItem, when: (iso: string) => string): string {
  if (e.status === "submitted") return "Submitted" + (e.submitted_at ? " " + when(e.submitted_at) : "");
  if (e.missing?.length) return "Missing: " + labelList(e.missing).join(", ");
  if (hasAnyContent(e)) return "Nothing missing";
  return "No entry yet";
}

export const siteWords = (e: EntryItem) => (e.site_exists ? (e.site_taken_down ? "Site taken down" : "Site live") : "No site yet");

/** rowAction is the row's one button: Include an excluded entry, Exclude an incomplete one. */
export function rowAction(e: EntryItem): "include" | "exclude" | null {
  if (e.excluded) return "include";
  if (e.incomplete) return "exclude";
  return null;
}

/** detailHref is one team's entry, inside the organiser area. */
export const detailHref = (slug: string, team: string) => "/e/" + encodeURIComponent(slug) + "/manage?s=entry&team=" + encodeURIComponent(team);
export const entriesHref = (slug: string, team?: string) => "/e/" + encodeURIComponent(slug) + "/manage?s=entries" + (team ? "&team=" + encodeURIComponent(team) : "");
export const entryRulesHref = (slug: string) => "/e/" + encodeURIComponent(slug) + "/manage?s=settings&tab=entries#entry-rules";
export const remindersHref = (slug: string) => "/e/" + encodeURIComponent(slug) + "/manage?s=settings&tab=reminders";
