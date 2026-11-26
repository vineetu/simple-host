// Settings' plain logic: its tabs, the old #hash links, entry-rule presets,
// reminder wording, sign-up question ids, rule-change impact and the
// webhook words (the old _settings-*.js tables, unchanged).

export const SETTINGS_TABS = ["signup", "teams", "entries", "reminders", "judging", "results", "details", "organisers", "links", "notifications", "danger"] as const;
export type SettingsTab = (typeof SETTINGS_TABS)[number];
export const TAB_LABEL: Record<SettingsTab, string> = {
  signup: "Sign-up",
  teams: "Teams",
  entries: "Entries",
  reminders: "Reminders",
  judging: "Judging",
  results: "Results",
  details: "Organiser details",
  organisers: "Organisers",
  links: "Links",
  notifications: "Notifications",
  danger: "Leave or delete",
};

/** An old link's #anchor (no tab= in it) opens the tab that holds that section. */
export const HASH_TAB: Record<string, SettingsTab> = {
  applications: "signup",
  signup: "signup",
  directory: "signup",
  deadline: "entries",
  "entry-rules": "entries",
  handin: "entries",
  reminders: "reminders",
  rubric: "judging",
  mode: "judging",
  "tie-break": "judging",
  closes: "judging",
  "organiser-judges": "judging",
  ranking: "results",
  voting: "results",
  coorg: "organisers",
  leave: "danger",
  delete: "danger",
};

// ---- entries: what teams hand in (server table: host/rules/hackentries Presets)

export type FieldMode = "required" | "optional" | "off" | "one_of";
export type Fields = Record<string, FieldMode>;

export const PRESETS: Record<string, { label: string; desc: string; fields: Fields; judge_first: "video" | "site" }> = {
  video_or_website: {
    label: "Video or website",
    desc: "Teams show a demo video or a published website, at least one of the two.",
    fields: { title: "required", problem: "required", tagline: "required", audience: "optional", track: "required", tools: "required", maturity: "required", description: "optional", video_url: "one_of", site: "one_of", code_url: "off", screenshot: "optional" },
    judge_first: "video",
  },
  video_website_code: {
    label: "Video, website and code link",
    desc: "Teams show a demo video, a published website and a code link, all three required.",
    fields: { title: "required", problem: "required", tagline: "required", audience: "optional", track: "required", tools: "required", maturity: "required", description: "optional", video_url: "required", site: "required", code_url: "required", screenshot: "optional" },
    judge_first: "site",
  },
};

export const SUMMARY_ROWS = [
  { id: "problem", label: "Problem, in one line" },
  { id: "tagline", label: "Solution, in one line", help: "Also the line on the team sites list" },
  { id: "audience", label: "Who it’s for" },
  { id: "track", label: "Track" },
  { id: "tools", label: "Tools and AI", help: "Tags, with suggestions" },
  { id: "maturity", label: "What works today", help: "Demo-ready, partly working or concept" },
];
export const SHOW_ROWS = [
  { id: "video_url", label: "Video link", help: "Plays on the judge’s screen when it’s YouTube, Vimeo, Loom or Drive" },
  { id: "site", label: "Website", help: "The team site, published from an AI app or a zip" },
  { id: "code_url", label: "Code link", help: "GitHub or similar" },
  { id: "screenshot", label: "Screenshot" },
];
export const SUMMARY_OPTS: [FieldMode, string][] = [
  ["required", "Required"],
  ["optional", "Optional"],
  ["off", "Off"],
];
export const SHOW_OPTS: [FieldMode, string][] = [
  ["required", "Required"],
  ["one_of", "One of"],
  ["optional", "Optional"],
  ["off", "Off"],
];

export function fieldsEqual(a: Fields = {}, b: Fields = {}): boolean {
  for (const k of new Set([...Object.keys(a), ...Object.keys(b)])) if ((a[k] || "optional") !== (b[k] || "optional")) return false;
  return true;
}

/** withField is the rules after one line changes: still the preset if every line matches it, else custom. */
export function withField(rules: { preset?: string; fields?: Fields }, field: string, mode: FieldMode): { preset: string; fields: Fields } {
  const fields = { ...(rules.fields || {}), [field]: mode };
  const base = PRESETS[rules.preset || ""];
  return { fields, preset: base && fieldsEqual(fields, base.fields) ? rules.preset! : "custom" };
}

/** presetLabel names what the rules are now: a preset, or Custom (from the last preset picked). */
export function presetLabel(preset: string | undefined, lastChosen: string | null): string {
  if (preset && preset !== "custom") return PRESETS[preset]?.label || preset;
  const base = lastChosen && PRESETS[lastChosen];
  return base ? `Custom (from ${base.label})` : "Custom";
}

/** impactLines are what a rule change does to entries already made (the dry run's answer); none means just save. */
export function impactLines(impact: { submitted_missing?: number; drafts_newly_blocked?: number } = {}): string[] {
  const out: string[] = [];
  const s = impact.submitted_missing || 0;
  const d = impact.drafts_newly_blocked || 0;
  if (s) out.push(`${s} submitted ${s === 1 ? "entry does" : "entries do"} not meet what you are about to ask for. They stay submitted, and their teams see that you now ask for more.`);
  if (d) out.push(`${d} draft${d === 1 ? "" : "s"} will need more before ${d === 1 ? "it can" : "they can"} submit.`);
  return out;
}

// ---- reminders

/** offsetLabel is "3 days", "1 hour", "90 minutes": minutes only when not a whole hour or day. */
export function offsetLabel(m: number): string {
  if (m >= 1440 && m % 1440 === 0) {
    const d = m / 1440;
    return d + (d === 1 ? " day" : " days");
  }
  if (m >= 60 && m % 60 === 0) {
    const h = m / 60;
    return h + (h === 1 ? " hour" : " hours");
  }
  return m + (m === 1 ? " minute" : " minutes");
}

export type ReminderLimits = { min_minutes?: number; max_minutes?: number; max_count?: number };

/** addReminder checks one new reminder ("2", "hours") against the list and limits; it returns the new list or the error line. */
export function addReminder(list: number[], raw: string, unit: "days" | "hours" | "minutes", lim: ReminderLimits): number[] | string {
  const n = Number(raw.trim());
  if (!raw.trim() || !Number.isInteger(n) || n <= 0) return "Enter a whole number.";
  const minutes = n * (unit === "days" ? 1440 : unit === "hours" ? 60 : 1);
  const min = lim.min_minutes ?? 1;
  const max = lim.max_minutes ?? 20160;
  if (minutes < min || minutes > max) return `Each reminder must be between ${min} minutes and 14 days before the deadline.`;
  if (list.includes(minutes)) return "Already have a reminder at that time.";
  if (list.length >= (lim.max_count ?? 5)) return `Up to ${lim.max_count ?? 5} reminders.`;
  return [...list, minutes].sort((a, b) => b - a);
}

export const sameOffsets = (a: number[], b: number[]) => JSON.stringify([...a].sort((x, y) => y - x)) === JSON.stringify([...b].sort((x, y) => y - x));

// ---- sign-up questions

export type Question = { id: string; prompt: string; required: boolean };

/** withIds gives each new question an id no other question uses (never by position: removals shift them). */
export function withIds(qs: Question[]): Question[] {
  const used = new Set(qs.map((q) => q.id).filter(Boolean));
  let n = 1;
  return qs.map((q) => {
    if (q.id) return q;
    while (used.has("q" + n)) n++;
    used.add("q" + n);
    return { ...q, id: "q" + n };
  });
}

// ---- rubric

export type Criterion = { id: string; name: string; description: string; weight: number; max_points: number };
export const weightTotal = (rubric: Criterion[]) => rubric.reduce((s, c) => s + (Number(c.weight) || 0), 0);

// ---- webhooks

export const NOTIFY_EVENTS: [string, string][] = [
  ["record.added", "A visitor saves something"],
  ["record.changed", "A visitor changes something they saved"],
  ["file.uploaded", "A visitor uploads a file"],
];
export const NOTIFY_STATES: Record<string, string> = { active: "Active", paused: "Paused", suspended: "Suspended after failures", pending: "Waiting for check", expired: "Expired" };
export const NOTIFY_RESULTS: Record<string, string> = {
  secret_unreadable: "Stored secret cannot be read with this server’s key; rotate it or add the notification again.",
  ok: "Delivered",
  connection_refused: "Connection refused",
  timeout: "Receiver took too long",
  tls_error: "Secure connection failed",
  http_4xx: "Receiver rejected the message",
  http_5xx: "Receiver had a server error",
  redirect: "Receiver redirected the request",
  refused_address: "Receiver address is not allowed",
  dns_error: "Receiver address could not be found",
  challenge_failed: "Receiver check failed",
  site_unavailable: "Site unavailable",
};
export const NOTIFY_DELIVERY_STATES: Record<string, string> = { pending: "Waiting for delivery", delivered: "Delivered", failed: "Failed", skipped: "Skipped" };
export const eventName = (e: string) => NOTIFY_EVENTS.find((x) => x[0] === e)?.[1] || e;

export type Hook = { id: string; url?: string; label?: string; source?: string; app_name?: string; events?: string[]; storage?: string; state?: string; last_attempt_at?: string; last_result?: string };

/** receiverName is how a webhook is named in its row: the app, its label, or its address's host. */
export function receiverName(n: Hook): string {
  if (n.source === "mcp") return n.app_name || "Connected app";
  if (n.label) return n.label;
  try {
    return new URL(n.url || "").host;
  } catch {
    return n.url || "";
  }
}

/** testLine is what a test delivery's answer says. */
export function testLine(r: { delivered?: boolean; result?: string; status?: number }): string {
  const base = r.delivered ? "Test delivered" : NOTIFY_RESULTS[r.result || ""] || "Could not deliver the test";
  return base.replace(/\.$/, "") + (r.status ? ` (HTTP ${r.status})` : "") + ".";
}
