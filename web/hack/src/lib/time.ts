// Dates and deadlines, as the old ui.js shows them: "Wed 28 Oct, 6:27 pm" in
// the event's own zone (year only when it is not this one), never seconds.
// Ported: dateTime, zonedDate (dateTime's zone label option), isLocalMidnight,
// nextLocalMidnight, nextDeadline, zoneAbbr, zoneOptions, browserZone. The one
// owner of dates and zones: screens import these, never their own copies.

type Zoned = { time_zone?: string } | string | undefined;
const zoneOf = (e: Zoned) => (typeof e === "string" ? e : e?.time_zone);

function zoneKnown(tz?: string): boolean {
  try {
    new Intl.DateTimeFormat("en-US", { timeZone: tz || "UTC" }).format(0);
    return true;
  } catch {
    return false;
  }
}

/** The wall-clock parts of an instant in a zone (UTC when the zone is unknown). */
export function zonedParts(iso: string | null | undefined, tz?: string) {
  const d = new Date(iso || "");
  if (!iso || Number.isNaN(d.getTime())) return null;
  const zone = zoneKnown(tz) ? tz || "UTC" : "UTC";
  const o: Record<string, string> = {};
  for (const p of new Intl.DateTimeFormat("en-US", { timeZone: zone, hourCycle: "h23", year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", second: "2-digit" }).formatToParts(d))
    o[p.type] = p.value;
  return { y: o.year, m: o.month, d: o.day, h: o.hour === "24" ? "00" : o.hour, min: o.minute, s: o.second };
}

const yearIn = (d: Date, zone?: string) => {
  try {
    return new Intl.DateTimeFormat("en-US", { year: "numeric", ...(zone && zoneKnown(zone) ? { timeZone: zone } : {}) }).format(d);
  } catch {
    return String(d.getFullYear());
  }
};

/** dateTime is the one date/time format every screen uses. */
export function dateTime(value: string | null | undefined, event?: Zoned, options: { dateOnly?: boolean } = {}): string {
  if (!value || Number.isNaN(new Date(value).getTime())) return "";
  const d = new Date(value);
  const zone = zoneOf(event);
  const sameYear = yearIn(d, zone) === yearIn(new Date(), zone);
  const dateOnlyValue = /^\d{4}-\d{2}-\d{2}$/.test(value);
  const o: Intl.DateTimeFormatOptions = {
    weekday: "short",
    day: "numeric",
    month: "short",
    ...(sameYear ? {} : { year: "numeric" }),
    ...(dateOnlyValue || options.dateOnly ? {} : { hour: "numeric", minute: "2-digit", hourCycle: "h12" }),
    ...(dateOnlyValue ? { timeZone: "UTC" } : zone && zoneKnown(zone) ? { timeZone: zone } : {}),
  };
  return new Intl.DateTimeFormat("en-GB", o).format(d).replace(/^(\S+),\s/, "$1 ");
}

export function isLocalMidnight(value: string, event?: Zoned): boolean {
  const p = zonedParts(value, zoneOf(event));
  return !!p && p.h === "00" && p.min === "00" && p.s === "00";
}

function offsetMinutes(zone: string, date: Date): number | null {
  try {
    const raw = new Intl.DateTimeFormat("en-US", { timeZone: zone, timeZoneName: "shortOffset" }).formatToParts(date).find((p) => p.type === "timeZoneName")?.value || "";
    const m = raw.match(/GMT([+-]\d{1,2})(?::?(\d{2}))?/);
    if (!m) return raw === "GMT" ? 0 : null;
    const sign = m[1].startsWith("-") ? -1 : 1;
    return sign * (Math.abs(Number(m[1])) * 60 + Number(m[2] || 0));
  } catch {
    return null;
  }
}

/** nextLocalMidnight is the instant a date-only end runs to: the next local midnight, DST-correct. */
export function nextLocalMidnight(value: string, event?: Zoned): number | null {
  const zone = zoneOf(event);
  const p = zonedParts(value, zone);
  if (!p) return null;
  const z = zoneKnown(zone) ? zone || "UTC" : "UTC";
  const t = new Date(Date.UTC(Number(p.y), Number(p.m) - 1, Number(p.d) + 1));
  const guess = Date.UTC(t.getUTCFullYear(), t.getUTCMonth(), t.getUTCDate());
  const o1 = offsetMinutes(z, new Date(guess));
  if (o1 == null) return guess;
  let instant = guess - o1 * 60000;
  const o2 = offsetMinutes(z, new Date(instant));
  if (o2 != null && o2 !== o1) instant = guess - o2 * 60000;
  return instant;
}

const BY_STAGE: Record<string, string[]> = {
  draft: ["Starts", "Entries close", "Ends"],
  open: ["Starts", "Entries close", "Ends"],
  building: ["Entries close", "Ends"],
  closed: ["Ends"],
  judging: ["Judging closes"],
};

type Dated = { stage?: string; time_zone?: string; starts_at?: string | null; submission_deadline?: string | null; ends_at?: string | null; judging_ends_at?: string | null };

/** nextDeadline is the next date that still matters at this stage, as [label, value, dateOnly], or null. */
export function nextDeadline(event: Dated, now = Date.now()): [string, string, boolean] | null {
  const wanted = BY_STAGE[event.stage || ""] || [];
  const rows = (
    [
      ["Starts", event.starts_at],
      ["Entries close", event.submission_deadline],
      ["Ends", event.ends_at],
      ["Judging closes", event.judging_ends_at],
    ] as [string, string | null | undefined][]
  )
    .filter(([label, v]) => wanted.includes(label) && v && !Number.isNaN(new Date(v).getTime()))
    .map(([label, v]) => {
      const dateOnly = label !== "Entries close" && isLocalMidnight(v!, event);
      const until = (label === "Ends" && dateOnly ? nextLocalMidnight(v!, event) : null) ?? new Date(v!).getTime();
      return { label, v: v!, dateOnly, until };
    })
    .filter((r) => r.until > now)
    .sort((a, b) => a.until - b.until);
  return rows.length ? [rows[0].label, rows[0].v, rows[0].dateOnly] : null;
}

// ---- Time zones (ui.js zoneAbbr, _orgA-util.js zoneOptions; owner decision 2026-10-30) ----
// A short familiar name where one exists, else "GMT+N", the same as the public event page.

const ZONE_ABBR: Record<string, string> = {
  "Asia/Jakarta": "WIB",
  "Asia/Pontianak": "WIB",
  "Asia/Makassar": "WITA",
  "Asia/Jayapura": "WIT",
  "Asia/Tokyo": "JST",
  "Asia/Seoul": "KST",
  "Asia/Singapore": "SGT",
  "Asia/Kolkata": "IST",
  "Asia/Calcutta": "IST",
};
const AU_ZONES: Record<string, string[]> = {
  "Australia/Sydney": ["AEST", "AEDT"],
  "Australia/Melbourne": ["AEST", "AEDT"],
  "Australia/Hobart": ["AEST", "AEDT"],
  "Australia/Canberra": ["AEST", "AEDT"],
  "Australia/Brisbane": ["AEST"],
  "Australia/Adelaide": ["ACST", "ACDT"],
  "Australia/Darwin": ["ACST"],
  "Australia/Perth": ["AWST"],
};
const CET_ZONES = [
  "Europe/Paris", "Europe/Berlin", "Europe/Madrid", "Europe/Rome", "Europe/Amsterdam", "Europe/Brussels", "Europe/Vienna",
  "Europe/Prague", "Europe/Warsaw", "Europe/Zurich", "Europe/Stockholm", "Europe/Copenhagen", "Europe/Budapest", "Europe/Luxembourg",
];
const FALLBACK_ZONES = [
  "UTC", "Asia/Jakarta", "Asia/Makassar", "Asia/Jayapura", "Asia/Tokyo", "Asia/Seoul", "Asia/Singapore", "Asia/Kolkata",
  "Europe/London", "Europe/Paris", "Europe/Berlin", "America/New_York", "America/Chicago", "America/Denver", "America/Los_Angeles", "Australia/Sydney",
];

function inDaylight(zone: string, date: Date): boolean {
  const jan = offsetMinutes(zone, new Date(date.getFullYear(), 0, 1, 12));
  const cur = offsetMinutes(zone, date);
  return jan != null && cur != null && cur !== jan;
}

/** zoneAbbr is a zone's short name now (WIB, CET, AEDT), or its GMT offset. */
export function zoneAbbr(zone: string | undefined, date = new Date(), known = false): string {
  if (!zone || (!known && !zoneKnown(zone))) return "";
  let raw = "";
  try {
    const parts = new Intl.DateTimeFormat("en-US", { timeZone: zone, timeZoneName: "short" }).formatToParts(date);
    raw = (parts.find((p) => p.type === "timeZoneName")?.value || "").trim();
    if (raw && !/^(GMT|UTC)?[+-]\d/.test(raw)) return raw;
  } catch {
    /* Intl without short names */
  }
  if (ZONE_ABBR[zone]) return ZONE_ABBR[zone];
  if (zone === "Europe/London" || zone === "Europe/Dublin") return inDaylight(zone, date) ? (zone === "Europe/Dublin" ? "IST" : "BST") : "GMT";
  if (CET_ZONES.includes(zone)) return inDaylight(zone, date) ? "CEST" : "CET";
  const au = AU_ZONES[zone];
  if (au) {
    if (!au[1]) return au[0];
    const jan = offsetMinutes(zone, new Date(date.getFullYear(), 0, 1, 12));
    const jul = offsetMinutes(zone, new Date(date.getFullYear(), 6, 1, 12));
    const cur = offsetMinutes(zone, date);
    return jan != null && jul != null && cur != null && cur > Math.min(jan, jul) ? au[1] : au[0];
  }
  const mins = offsetMinutes(zone, date);
  if (mins == null) return raw;
  if (mins === 0) return "GMT";
  const abs = Math.abs(mins);
  return "GMT" + (mins < 0 ? "-" : "+") + Math.floor(abs / 60) + (abs % 60 ? ":" + String(abs % 60).padStart(2, "0") : "");
}

function offsetLabel(m: number | null): string {
  if (!m) return ""; // GMT itself has no offset label, as before
  const abs = Math.abs(m);
  return "UTC" + (m < 0 ? "-" : "+") + Math.floor(abs / 60) + (abs % 60 ? ":" + String(abs % 60).padStart(2, "0") : "");
}

/** zoneOptions are the time-zone choices ("Asia/Jakarta · WIB, UTC+7"), always including current. */
export function zoneOptions(current: string, now = new Date()): { zone: string; label: string }[] {
  let zones: string[] | null = null;
  try {
    zones = typeof Intl.supportedValuesOf === "function" ? Intl.supportedValuesOf("timeZone") : null;
  } catch {
    zones = null;
  }
  const listed = !!zones?.length;
  if (!zones || !zones.length) zones = FALLBACK_ZONES;
  const extra = current && !zones.includes(current) ? current : "";
  if (extra) zones = [extra, ...zones];
  return zones
    .slice()
    .sort()
    .map((z) => {
      const abbr = zoneAbbr(z, now, listed && z !== extra);
      const off = offsetLabel(offsetMinutes(z, now));
      return { zone: z, label: z + (abbr || off ? " · " + [abbr, off].filter(Boolean).join(", ") : "") };
    });
}

/** browserZone is this browser's time zone, the form's default. */
export function browserZone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
  } catch {
    return "UTC";
  }
}

/** zonedDate is dateTime with the zone label after a time of day ("Fri 6 Nov, 5:00 am WIB"; ui.js dateTime {zone:true}). */
export function zonedDate(value: string | null | undefined, event?: Zoned, options: { dateOnly?: boolean } = {}): string {
  if (!value) return "";
  const zone = zoneOf(event);
  const dateOnly = !!options.dateOnly || /^\d{4}-\d{2}-\d{2}$/.test(value);
  const base = dateTime(value, zone, { dateOnly });
  if (dateOnly || !base || !zone) return base;
  const label = zoneAbbr(zone, new Date(value));
  return label ? base + " " + label : base;
}
