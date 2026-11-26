// Event page's plain logic: its tabs and old links, the content document
// (sponsors, FAQ, schedule) and the schedule's fixed rows (schedule.js
// merge/label/setInLink), the Look choices, and the zip refusal wording.
import { ApiError } from "@kit/api/call";
import { dateTime, nextLocalMidnight } from "@/lib/time";
import { href } from "@/routes/resolve";

export const TABS = ["basics", "about", "schedule", "tracks", "sponsors", "faq", "look", "website"] as const;
export type Tab = (typeof TABS)[number];
export const TAB_LABEL: Record<Tab, string> = { basics: "Basics", about: "About", schedule: "Schedule", tracks: "Tracks", sponsors: "Sponsors", faq: "FAQ", look: "Look", website: "Website" };
/** Old #hash links (emails may carry them): #appearance is Look, #address is Website. */
export const HASH_TAB: Record<string, Tab> = { basics: "basics", schedule: "schedule", sponsors: "sponsors", faq: "faq", appearance: "look", address: "website" };

/** wantsAnnouncements: the old Announcements part of this page (tab= or #announcements) now has its own screen. */
export function wantsAnnouncements(search: string, hash: string): boolean {
  const tab = new URLSearchParams(search).get("tab") || "";
  if (tab === "announcements") return true;
  let h = "";
  try {
    h = decodeURIComponent(hash.slice(1));
  } catch {
    h = "";
  }
  return !(TABS as readonly string[]).includes(tab) && h === "announcements";
}

export type Sponsor = { name: string; tier?: string; url?: string; logo_data?: string };
export type Faq = { question: string; answer: string };
export type ScheduleItem = { title: string; description?: string; start_at: string; end_at?: string | null; prompt?: string };
export type FixedRow = { key: string; title: string; description?: string; start_at: string; end_at?: string | null; all_day?: boolean; fixed: true; set_in?: string; prompt?: string };
export type Content = { sponsors: Sponsor[]; faq: Faq[]; schedule: ScheduleItem[] };
export type ContentAnswer = Partial<Content> & { fixed_schedule?: FixedRow[] };

/** doc is the part of GET/PUT /content that is written back (never the fixed rows). */
export const doc = (c: ContentAnswer | undefined): Content => ({ sponsors: c?.sponsors || [], faq: c?.faq || [], schedule: c?.schedule || [] });

/** The forward moves a schedule row may ask for when it starts (hack_schedule.go hackScheduleMoves). */
export const MOVES: Record<string, string> = {
  open: "Open sign-up",
  building: "Start building",
  closed: "Close entries",
  judging: "Start judging",
  results: "Finish judging",
  publish_results: "Publish results",
};

/** setInLink is where a fixed row's setting is changed. */
export function setInLink(setIn: string | undefined, slug: string, key: string): { label: string; href: string } | null {
  if (key === "voting") return { label: "Settings → Results", href: href("organiser", slug, "settings") + "&tab=results#voting" };
  if (setIn === "basics") return { label: "Basics", href: href("organiser", slug, "event-page") + "&tab=basics" };
  if (setIn === "entries") return { label: "Settings → Entries", href: href("organiser", slug, "settings") + "&tab=entries#deadline" };
  if (setIn === "results") return { label: "Results", href: href("organiser", slug, "results") };
  if (setIn === "judging") return { label: "Settings → Judging", href: href("organiser", slug, "settings") + "&tab=judging#closes" };
  return null;
}

export type Row = (ScheduleItem | FixedRow) & { fixed: boolean; all_day?: boolean; key?: string; set_in?: string; src?: ScheduleItem; now?: boolean; next?: boolean };

const ms = (iso: string) => new Date(iso).getTime();
const valid = (iso?: string | null) => !!iso && !Number.isNaN(ms(iso));

// A date-only event end runs to the end of its day, so it sorts after that day's other rows.
function sortAt(r: Row, zone?: string): number {
  const t = ms(r.start_at);
  if (!(r.all_day && r.key === "event_end")) return t;
  const next = nextLocalMidnight(r.start_at, zone);
  return next != null ? next - 1000 : t + 86399000;
}

/** merge is one list in time order; typed rows keep the stored object (src) so an edit finds it by identity. */
export function merge(schedule: ScheduleItem[], fixed: FixedRow[], zone?: string): Row[] {
  const rows: Row[] = [];
  for (const it of schedule) if (valid(it.start_at)) rows.push({ ...it, fixed: false, all_day: false, src: it });
  for (const it of fixed) if (valid(it.start_at)) rows.push({ ...it, fixed: true });
  return rows.sort((a, b) => sortAt(a, zone) - sortAt(b, zone));
}

/** label marks Now (a window holding this moment) and Next (the first still to start); date-only rows are neither. */
export function label(rows: Row[], now = Date.now()): Row[] {
  let nowIdx = -1;
  let nextIdx = -1;
  rows.forEach((r, i) => {
    r.now = false;
    r.next = false;
    if (r.all_day) return;
    const s = ms(r.start_at);
    const e = valid(r.end_at) ? ms(r.end_at!) : null;
    if (nowIdx < 0 && e !== null && s <= now && now < e) nowIdx = i;
    if (nextIdx < 0 && s > now) nextIdx = i;
  });
  if (nowIdx >= 0) rows[nowIdx].now = true;
  if (nextIdx >= 0) rows[nextIdx].next = true;
  return rows;
}

/** timeOnly is the time half of dateTime ("6:00 pm"). */
export function timeOnly(iso: string, zone?: string): string {
  const full = dateTime(iso, zone);
  const i = full.lastIndexOf(", ");
  return i >= 0 ? full.slice(i + 2) : full;
}
export const dayOf = (iso: string, zone?: string) => dateTime(iso, zone, { dateOnly: true });

/** when is a row's time: "6:00 pm – 7:00 pm", "– Sun 6:00 pm" on another day, or All day. */
export function when(r: Row, zone?: string): [string, string] {
  if (r.all_day) return ["All day", ""];
  const start = timeOnly(r.start_at, zone);
  if (!valid(r.end_at)) return [start, ""];
  const end = dayOf(r.start_at, zone) === dayOf(r.end_at!, zone) ? timeOnly(r.end_at!, zone) : dayOf(r.end_at!, zone).replace(/^(\S+)\s.*/, "$1") + " " + timeOnly(r.end_at!, zone);
  return [start, "– " + end];
}

/** groupByDay splits the rows under their day headers, in order. */
export function groupByDay(rows: Row[], zone?: string): { day: string; rows: Row[] }[] {
  const out: { day: string; rows: Row[] }[] = [];
  for (const r of rows) {
    const day = dayOf(r.start_at, zone);
    if (!out.length || out[out.length - 1].day !== day) out.push({ day, rows: [] });
    out[out.length - 1].rows.push(r);
  }
  return out;
}

/** checkTime is the schedule dialog's check: the error for each field, or none. */
export function checkTime(title: string, sd: string, st: string, ed: string, et: string): { title?: string; end?: string } {
  const errs: { title?: string; end?: string } = {};
  if (!title.trim()) errs.title = "Title is required.";
  const s = sd && st ? sd + "T" + st : "";
  const e = ed && et ? ed + "T" + et : "";
  if (!s || !e) errs.end = "Starts and ends are both required.";
  else if (e <= s) errs.end = "Ends must be after it starts.";
  return errs;
}

/** move swaps item i with its neighbour (dir -1 up, +1 down); the same list when it cannot move. */
export function move<T>(arr: T[], i: number, dir: number): T[] {
  const j = i + dir;
  if (j < 0 || j >= arr.length) return arr;
  const next = arr.slice();
  [next[i], next[j]] = [next[j], next[i]];
  return next;
}

/** moveTo takes item from and puts it at to (a drag). */
export function moveTo<T>(arr: T[], from: number, to: number): T[] {
  if (from === to || from < 0 || to < 0 || from >= arr.length || to >= arr.length) return arr;
  const next = arr.slice();
  const [it] = next.splice(from, 1);
  next.splice(to, 0, it);
  return next;
}

/** sponsorLine is "Tier · link host", or "No link". */
export function sponsorLine(s: Sponsor): string {
  let host = "No link";
  if (s.url) {
    try {
      host = new URL(s.url).host;
    } catch {
      host = s.url;
    }
  }
  return [s.tier, host].filter(Boolean).join(" · ") || "No link";
}

export const LOGO_TYPES = ["image/png", "image/jpeg", "image/webp"];
/** checkImage is why a picked logo or icon cannot be used ('' when it can). */
export function checkImage(file: { type: string; size: number }, maxKB = 128): string {
  if (!LOGO_TYPES.includes(file.type)) return "Use a PNG, JPEG or WebP image.";
  if (file.size > maxKB * 1024) return `Logo must be under ${maxKB} KB.`;
  return "";
}

/** slugify is a track's address name from its name (_orgB-util.js). */
export function slugify(name: string): string {
  let s = String(name || "")
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "");
  if (s.length > 80) s = s.slice(0, 80).replace(/-+$/, "");
  return s || "track-" + Math.random().toString(36).slice(2, 8);
}

// ---- Look

export const CRAYONS: [string, string][] = [["", "Automatic"], ["red", "Red"], ["teal", "Teal"], ["indigo", "Indigo"], ["orange", "Orange"], ["yellow", "Yellow"], ["grass", "Grass"], ["blush", "Blush"]];
export const SWATCH_HEX: Record<string, string> = { red: "#d15d48", teal: "#267b79", indigo: "#4a49a8", orange: "#e9a45f", yellow: "#f2c94c", grass: "#86ab74", blush: "#ee8f92" };
/** The public page's styles; '' (Classic) is the plain look (host/hackhttp/hack_themes.go is the allowlist). */
export const THEMES: [string, string, string][] = [
  ["", "Classic", "Today's look"],
  ["poster", "Poster", "Bold, for student hacks"],
  ["confetti", "Confetti", "Bright and playful"],
  ["boardroom", "Boardroom", "Clean, for company events"],
  ["garden", "Garden", "Calm and friendly"],
  ["terminal", "Terminal", "For developer crowds"],
];

/** anotherTheme is a random style other than the one showing (Try another). */
export function anotherTheme(current: string, rnd = Math.random): string {
  const opts = THEMES.map(([v]) => v).filter((v) => v && v !== current);
  return opts[Math.floor(rnd() * opts.length)];
}

// ---- Website

export type Version = { version_number: number; created_at: string };
export type Website = { mode?: string; has_custom_page?: boolean; versions?: Version[]; active_version?: number; upload_limits?: string; site?: string; owner?: string };

/** archiveErrorText is a zip refusal in plain words (the extractor's own text is server debug). */
export function archiveErrorText(err: unknown): string {
  if (err instanceof ApiError) {
    if (err.code === "missing_index") return "Put index.html at the top of the zip, not inside a folder.";
    if (err.code === "invalid_archive") {
      const raw = String(err.body?.error || err.message || "");
      if (/unsafe path|duplicate (entry|path)/i.test(raw))
        return "That zip has file paths Simple Host can't accept (a path outside the archive, or two files at the same path). Fix the archive and try again.";
      return "That file isn't a valid .zip.";
    }
    return err.message || "Something went wrong.";
  }
  return (err as Error)?.message || "Something went wrong.";
}

/** activeVersion is the version showing at the address (the newest when none is marked). */
export function activeVersion(w: Website): Version | undefined {
  const vs = w.versions || [];
  const num = w.active_version || vs[vs.length - 1]?.version_number;
  return vs.find((v) => v.version_number === num) || vs[vs.length - 1];
}
