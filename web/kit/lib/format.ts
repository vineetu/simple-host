// Plain formatting and matching used by the screens. Kept free of React so it is unit-tested.

const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

/** bytes is a size for people: 512 B, 12 KB, 3.4 MB, 1.2 GB, 1.25 TB. */
export function bytes(n: unknown): string {
  const v = Number(n) || 0;
  if (v >= 1099511627776) return (v / 1099511627776).toFixed(2).replace(/\.?0+$/, "") + " TB";
  if (v >= 1073741824) return (v / 1073741824).toFixed(1) + " GB";
  if (v >= 1048576) return (v / 1048576).toFixed(1) + " MB";
  if (v >= 1024) return Math.round(v / 1024) + " KB";
  return v + " B";
}

/** num is a count with thousands separators. */
export function num(n: unknown): string {
  return Number(n || 0).toLocaleString("en-US");
}

/** short is a count for a chart axis: 950, 1.2k, 15k, 2.5M. */
export function short(n: number): string {
  if (n >= 1e6) return (n / 1e6).toFixed(n >= 1e7 ? 0 : 1).replace(/\.0$/, "") + "M";
  if (n >= 1e4) return Math.round(n / 1e3) + "k";
  if (n >= 1e3) return (n / 1e3).toFixed(1).replace(/\.0$/, "") + "k";
  return String(n);
}

/** dateOf is "25 Nov 2026", or "" for no date. */
export function dateOf(s: string | null | undefined): string {
  const d = new Date(s || "");
  if (!s || isNaN(d.getTime())) return "";
  return d.getDate() + " " + MONTHS[d.getMonth()] + " " + d.getFullYear();
}

/** dayLabel is a UTC day ("2026-11-25") as "25 Nov", with the year when asked. */
export function dayLabel(s: string, withYear = false): string {
  const [y, m, d] = s.split("-").map(Number);
  return d + " " + MONTHS[m - 1] + (withYear ? " " + y : "");
}

/** ago is how long before now a time was, in calendar days: today, yesterday, 3 days ago, 2 months ago. */
export function ago(s: string | null | undefined, now = new Date()): string {
  const d = new Date(s || "");
  if (!s || isNaN(d.getTime())) return "";
  const a = new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
  const b = new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime();
  const days = Math.round((b - a) / 86400000);
  if (days <= 0) return "today";
  if (days === 1) return "yesterday";
  if (days < 30) return days + " days ago";
  if (days < 365) {
    const m = Math.max(1, Math.floor(days / 30.44));
    return m + " month" + (m === 1 ? "" : "s") + " ago";
  }
  const y = Math.max(1, Math.floor(days / 365.25));
  return y + " year" + (y === 1 ? "" : "s") + " ago";
}

/** searchMatches: every plain word must appear in text; a -word hides rows that contain it. */
export function searchMatches(query: string, text: string): boolean {
  const hay = text.toLowerCase();
  return query
    .toLowerCase()
    .split(/\s+/)
    .every((w) => {
      if (!w.startsWith("-")) return !w || hay.includes(w);
      const not = w.slice(1);
      return !not || !hay.includes(not);
    });
}

/** localPath is a path on this host only (the same rule as web.LocalPath in Go). */
export function localPath(s: string): boolean {
  // eslint-disable-next-line no-control-regex
  return /^\/(?![/\\])[^\\\s\u0000-\u001f\u007f-\u009f]*$/.test(s);
}

/** plural is "1 site", "3 sites". */
export function plural(n: number, word: string): string {
  return num(n) + " " + word + (n === 1 ? "" : "s");
}
