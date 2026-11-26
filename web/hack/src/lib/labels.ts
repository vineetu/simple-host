// Plain words shared by every screen (the old ui.js helpers of the same names).
import type { Role } from "@/lib/client";

export const STAGES: Record<string, string> = {
  draft: "Not open yet",
  open: "Sign-up open",
  building: "Building",
  closed: "Entries closed",
  judging: "Judging",
  results: "Results",
  archived: "Ended",
  ended: "Ended",
};
export const stageLabel = (stage?: string) => (stage ? STAGES[stage] || stage : "");
export const roleLabel = (role?: string) => ({ organiser: "Organiser", participant: "Participant", judge: "Judge" })[role as Role] || "";

/** initials are the first letters of an event's first two words ("Mock · Voting" is MV). */
export const initials = (name: string) =>
  String(name || "")
    .split(/\s+/)
    .filter((w) => /[\p{L}\p{N}]/u.test(w))
    .slice(0, 2)
    .map((w) => w.match(/[\p{L}\p{N}]/u)![0])
    .join("")
    .toUpperCase();

const lum = (color: string) => {
  const n = parseInt(color.slice(1), 16);
  const f = (v: number) => ((v /= 255), v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4);
  return 0.2126 * f(n >> 16) + 0.7152 * f((n >> 8) & 255) + 0.0722 * f(n & 255);
};
const contrast = (a: number, b: number) => (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05);

/** markInk is white or dark ink, whichever reads on the event's colour. */
export function markInk(color: string): string {
  return lum(color) > 0.45 ? "#2b2925" : "#fff";
}

/** markColours is the event's colour and the ink for its initials, the colour
 *  darkened a little only when neither ink would pass WCAG AA on it. */
export function markColours(color: string): { bg: string; ink: string } {
  const INK = "#2b2925";
  let bg = color;
  for (let i = 0; i < 12; i++) {
    const l = lum(bg);
    if (contrast(l, lum(INK)) >= 4.5) return { bg, ink: INK };
    if (contrast(l, 1) >= 4.5) return { bg, ink: "#fff" };
    const n = parseInt(bg.slice(1), 16);
    bg = "#" + [n >> 16, (n >> 8) & 255, n & 255].map((v) => Math.round(v * 0.92).toString(16).padStart(2, "0")).join("");
  }
  return { bg, ink: "#fff" };
}

/** safeHref is the link if it is http(s), else "" (javascript: and data: never pass). */
export const safeHref = (url?: string) => (/^https?:\/\//i.test(String(url || "").trim()) ? String(url).trim() : "");
/** safeLink is safeHref that also keeps a path on this site ("/e/x/apply"), never "//host" or a script URL. */
export const safeLink = (url?: string) => {
  const u = String(url || "").trim();
  return /^\/(?![/\\])/.test(u) ? u : safeHref(u);
};

/** possessive: "Ayu's", "Chris'". */
export const possessive = (name: string) => (name ? name + (/s$/i.test(name) ? "’" : "’s") : "");

/** joinNames: "Ayu", "Ayu and Nadia", "Ayu, Nadia and 2 others"; "They" for none. */
export function joinNames(names: (string | undefined)[] | undefined): string {
  const list = (names || []).filter(Boolean) as string[];
  if (!list.length) return "They";
  if (list.length === 1) return list[0];
  return list.slice(0, -1).join(", ") + " and " + list[list.length - 1];
}

export const MATURITY: Record<string, { label: string; note: string }> = {
  working: { label: "Demo-ready", note: "works end to end" },
  partial: { label: "Partly working", note: "some parts are mocked" },
  concept: { label: "Concept", note: "designs or a plan, nothing runs yet" },
};
export const AI_TOOLS = ["Claude", "ChatGPT", "Gemini", "Lovable", "Copilot", "Cursor"];
