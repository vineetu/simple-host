// The organiser's Ask panel, its plain logic (the old assistant.js, same
// rules): the per-person, per-event history in this browser, the limits the
// API sets on a question list, the starter questions, the limit lines, the
// safe reading of a reply (never HTML: paragraphs, lists, code, bold,
// italic, and links only into this event or the skills), the SSE reader,
// and the one place a question goes out (transport, which the catalogue
// swaps for a fake stream).
import { apiKey } from "@/lib/session";

export type Msg = { role: "user" | "assistant"; content: string };

export const MAX_MESSAGES = 20;
export const MAX_TOTAL_CHARS = 12000;
export const MAX_MSG_CHARS = 2000;

/** cpLen counts code points, not UTF-16 units (emoji and non-Latin text count once). */
export const cpLen = (s: string) => [...String(s || "")].length;

/** storageKey: per signed-in person and event, so two people sharing a browser never see each other's questions. */
export const storageKey = (slug: string, userId?: string | null) => (userId ? "sh-assistant-" + userId + "-" + slug : "sh-assistant-" + slug);

export function loadHistory(key: string): Msg[] {
  try {
    const raw = localStorage.getItem(key);
    if (!raw) return [];
    const parsed = JSON.parse(raw) as { messages?: unknown };
    if (!parsed || !Array.isArray(parsed.messages)) return [];
    return (parsed.messages as Msg[]).filter((m) => m && (m.role === "user" || m.role === "assistant") && typeof m.content === "string");
  } catch {
    return [];
  }
}
export function saveHistory(key: string, messages: Msg[]): void {
  try {
    localStorage.setItem(key, JSON.stringify({ messages, updated: Date.now() }));
  } catch {
    /* storage blocked */
  }
}
export function clearHistory(key: string): void {
  try {
    localStorage.removeItem(key);
  } catch {
    /* storage blocked */
  }
}

/** capMessages trims oldest first to the API's limits (20 messages, 12,000 code points), starting on a question. */
export function capMessages(messages: Msg[]): Msg[] {
  const list = messages.slice();
  while (list.length > MAX_MESSAGES) list.shift();
  let total = list.reduce((n, m) => n + cpLen(m.content), 0);
  while (total > MAX_TOTAL_CHARS && list.length > 0) {
    total -= cpLen(list[0].content);
    list.shift();
  }
  while (list.length && list[0].role === "assistant") list.shift();
  return list;
}

// ---- starters and limits -------------------------------------------------

function stageChip(stage?: string): string {
  if (stage === "draft" || stage === "open") return "Is my setup done?";
  if (stage === "building") return "What happens when I close entries?";
  if (stage === "closed" || stage === "judging") return "How do I finish judging?";
  if (stage === "results") return "How do I publish results?";
  if (stage === "archived" || stage === "ended") return "Where do I download the results?";
  return "Is my setup done?";
}

type Checklist = { next?: { label?: string } | null; items?: { label?: string; state?: string }[] } | null | undefined;

/** starterChips: one for the stage, one about the next checklist item, and the links one; no repeats. */
export function starterChips(stage?: string, checklist?: Checklist): string[] {
  const items = checklist?.items || [];
  const label = checklist?.next?.label || items.find((it) => it.state !== "done")?.label;
  const raw = [stageChip(stage), label ? 'What does "' + label + '" mean?' : "What should I do next?", "Where do I find the join and judge links?"];
  return [...new Set(raw)];
}

const MONTHS = ["January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"];
/** formatResetDate: "2026-12-01" is "1 December". */
export function formatResetDate(iso?: string): string {
  const m = /^(\d{4})-(\d{2})-(\d{2})/.exec(String(iso || ""));
  if (!m) return String(iso || "");
  const month = MONTHS[parseInt(m[2], 10) - 1];
  return month ? parseInt(m[3], 10) + " " + month : String(iso);
}
export const monthlyBudgetLine = (resets?: string) => "The assistant has used this month's budget. It's back on " + formatResetDate(resets) + ".";
export const dailyLimitLine = (dailyLimit?: number) => "You've asked " + dailyLimit + " questions today. The limit resets at midnight UTC.";

/** Status is GET E/assistant. */
export type Status = { enabled?: boolean; remaining_today?: number; daily_limit?: number; budget_reached?: boolean; resets?: string };

/** limitLine is the line that stops questions, from the status, or "". */
export function limitLine(s: Status): string {
  if (s.budget_reached) return monthlyBudgetLine(s.resets);
  if (s.remaining_today === 0) return dailyLimitLine(s.daily_limit);
  return "";
}

// ---- reading a reply -------------------------------------------------------

export type Inline = { t: "text" | "code" | "strong" | "em"; text: string } | { t: "a"; text: string; href: string };
export type Block = { t: "p"; lines: Inline[][] } | { t: "ul" | "ol"; items: Inline[][] } | { t: "pre"; text: string };

/** normaliseHref drops spaces and invisible characters models put in a link, and a doubled leading slash. */
export function normaliseHref(href: string): string {
  const h = String(href || "").replace(/[\s\u200b-\u200f\u2060\ufeff]/g, "");
  return /^\/\/(e|v1\/skills)\//.test(h) ? h.slice(1) : h;
}
/** allowedHref: only into this event's own pages or the skills, never out of the app. */
export const allowedHref = (h: string, slug: string) => (h.startsWith("/e/" + slug + "/") || h.startsWith("/v1/skills/")) && !h.includes("..");

const INLINE = /`([^`]+)`|\[([^\]]+)\]\(([^)]+)\)|\*\*([^*]+)\*\*|\*([^*]+)\*/g;

export function inline(text: string, slug: string): Inline[] {
  const out: Inline[] = [];
  let last = 0;
  let m: RegExpExecArray | null;
  INLINE.lastIndex = 0;
  while ((m = INLINE.exec(text))) {
    if (m.index > last) out.push({ t: "text", text: text.slice(last, m.index) });
    if (m[1] !== undefined) out.push({ t: "code", text: m[1] });
    else if (m[2] !== undefined) {
      const href = normaliseHref(m[3]);
      out.push(allowedHref(href, slug) ? { t: "a", text: m[2], href } : { t: "text", text: m[2] });
    } else if (m[4] !== undefined) out.push({ t: "strong", text: m[4] });
    else if (m[5] !== undefined) out.push({ t: "em", text: m[5] });
    last = INLINE.lastIndex;
  }
  if (last < text.length) out.push({ t: "text", text: text.slice(last) });
  return out;
}

const BULLET = /^[-*]\s+/;
const NUMBER = /^\d+\.\s+/;

/** blocks reads a reply's markdown into paragraphs, lists and code (nothing else is ever rendered). */
export function blocks(text: string, slug: string): Block[] {
  const lines = String(text || "").split("\n");
  const out: Block[] = [];
  let i = 0;
  while (i < lines.length) {
    const line = lines[i];
    if (/^```/.test(line)) {
      i++;
      const code: string[] = [];
      while (i < lines.length && !/^```/.test(lines[i])) code.push(lines[i++]);
      i++;
      out.push({ t: "pre", text: code.join("\n") });
      continue;
    }
    if (line.trim() === "") {
      i++;
      continue;
    }
    for (const [re, t] of [
      [BULLET, "ul"],
      [NUMBER, "ol"],
    ] as const)
      if (re.test(line)) {
        const items: Inline[][] = [];
        while (i < lines.length && re.test(lines[i])) items.push(inline(lines[i++].replace(re, ""), slug));
        out.push({ t, items });
        break;
      }
    if (BULLET.test(line) || NUMBER.test(line)) continue;
    const para: Inline[][] = [];
    while (i < lines.length && lines[i].trim() !== "" && !/^```/.test(lines[i]) && !BULLET.test(lines[i]) && !NUMBER.test(lines[i])) para.push(inline(lines[i++], slug));
    out.push({ t: "p", lines: para });
  }
  return out;
}

// ---- the stream ------------------------------------------------------------

export type StreamEvent = { delta?: string; done?: boolean; remaining_today?: number; error?: boolean | string; message?: string };

/** readSSE reads `data: {json}` events, in order, until the stream ends. */
export async function readSSE(body: ReadableStream<Uint8Array>, onEvent: (e: StreamEvent) => void): Promise<void> {
  const reader = body.getReader();
  const decoder = new TextDecoder();
  let buf = "";
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    buf += decoder.decode(value, { stream: true });
    let idx: number;
    while ((idx = buf.indexOf("\n\n")) !== -1) {
      const chunk = buf.slice(0, idx);
      buf = buf.slice(idx + 2);
      const line = chunk.split("\n").find((l) => l.startsWith("data:"));
      const json = line?.slice(5).trim();
      if (!json) continue;
      try {
        onEvent(JSON.parse(json) as StreamEvent);
      } catch {
        /* not JSON: skip */
      }
    }
  }
}

/** transport.send posts a question list (POST E/assistant, signed with the key). The catalogue replaces send with a fake stream. */
export const transport = {
  send(path: string, body: unknown, signal: AbortSignal): Promise<Response> {
    const headers: Record<string, string> = { "Content-Type": "application/json" };
    const k = apiKey();
    if (k) headers["X-API-Key"] = k;
    return fetch(path, { method: "POST", headers, body: JSON.stringify(body), signal });
  },
};

/** sse makes a Response whose body streams these events (then ends, unless open), for the catalogue and tests. */
export function sse(events: StreamEvent[], open = false): Response {
  const enc = new TextEncoder();
  const stream = new ReadableStream<Uint8Array>({
    start(c) {
      for (const e of events) c.enqueue(enc.encode("data: " + JSON.stringify(e) + "\n\n"));
      if (!open) c.close();
    },
  });
  return new Response(stream, { status: 200, headers: { "Content-Type": "text/event-stream" } });
}
