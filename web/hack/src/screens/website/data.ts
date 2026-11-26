// The Website screen's plain logic (participant-website.js): GET E/site's
// shape, the state strip's two lines, the ready prompt, the zip's refusals
// in plain words, and the webhook labels.
import { ApiError } from "@kit/api/call";
import { dateTime } from "@/lib/time";
import { ago, say } from "../team/words";

export type Site = {
  exists?: boolean;
  ready?: boolean;
  url?: string;
  live_version?: number | null;
  taken_down?: boolean;
  taken_down_reason?: string;
  published_at?: string | null;
  published_via?: string | null;
  pinned_version?: number | null;
  pinned_at?: string | null;
};
export type Connection = { state: "none" | "connected" | string; app?: string | null; apps?: string[]; last_used_at?: string | null; teammates_connected?: string[] };
/** GET E/site. */
export type SiteStatus = { site?: Site; connection?: Connection; api_base?: string; mcp_url?: string; upload_limits?: string; upload_limit_bytes?: number };

export const POLL_MS = 5000;
export const POLL_LIMIT_MS = 10 * 60 * 1000;

export const hostOnly = (url?: string) => String(url || "").replace(/^https?:\/\//, "");

const apps = (c: Connection) => (c.apps && c.apps.length ? c.apps : [c.app || "your AI app"]);
/** joinedApps: "Claude", "Claude and Bolt", "Claude, Bolt and Cursor". */
export function joinedApps(c: Connection): string {
  const n = apps(c);
  return n.length < 2 ? n[0] : n.slice(0, -1).join(", ") + " and " + n[n.length - 1];
}
export const connectedTo = (c: Connection, label: string) => c.state === "connected" && apps(c).some((a) => a.toLowerCase() === label.toLowerCase());

function teammates(names: string[]): string {
  if (names.length === 1) return names[0] + " is connected too";
  if (names.length === 2) return names[0] + " and " + names[1] + " are connected too";
  return names.slice(0, -1).join(", ") + " and " + names[names.length - 1] + " are connected too";
}

export type Cell = { on: "" | "on" | "wait"; label: string; sub: string; link?: string };

/** strip is the two state lines: the AI app and the website. */
export function strip(site: Site, conn: Connection, closed: boolean, now = Date.now()): { app: Cell; web: Cell } {
  let app: Cell;
  if (conn.state === "connected") {
    const mates = conn.teammates_connected || [];
    app = { on: "on", label: "Connected in " + joinedApps(conn), sub: "Last used " + ago(conn.last_used_at, now) + (mates.length ? " · " + teammates(mates) : "") };
  } else if (closed) app = { on: "", label: "Not connected", sub: "Publishing is closed." };
  else if (site.exists) app = { on: "", label: "Not connected", sub: "To publish from an AI app, open Publish again below." };
  else app = { on: "", label: "Not connected yet", sub: "Follow the steps below once. It takes about two minutes." };

  let web: Cell;
  if (!site.ready) web = { on: "", label: "Setting up", sub: "Your address is being set up. It is usually ready within a few minutes." };
  else if (site.taken_down) web = { on: "", label: "Taken down", sub: String(site.taken_down_reason || "").trim() || "Taken down by Simple Host." };
  else if (site.exists)
    web = {
      on: "on",
      label: "Published" + (typeof site.live_version === "number" ? " · version " + site.live_version : ""),
      sub: ago(site.published_at, now),
      link: site.url,
    };
  else if (closed)
    web = site.pinned_version
      ? { on: "", label: "Submitted version " + site.pinned_version, sub: site.pinned_at ? "Pinned " + dateTime(site.pinned_at) : "" }
      : { on: "", label: "Not published", sub: "Nothing was published by the deadline." };
  else web = { on: "wait", label: "Not published yet", sub: "Your address is ready: " + hostOnly(site.url) };
  return { app, web };
}

/** siteLine: "Version 3, published via claude, 2 hours ago. Judges see the version that is live at your deadline." */
export function siteLine(site: Site, now = Date.now()): string {
  const bits: string[] = [];
  if (typeof site.live_version === "number") bits.push("Version " + site.live_version);
  if (site.published_via) bits.push("published via " + site.published_via);
  let sub = bits.join(", ");
  const when = ago(site.published_at, now);
  if (when) sub += (sub ? ", " : "") + when;
  return sub + (sub ? ". " : "") + "Judges see the version that is live at your deadline.";
}

const end = (t: string) => (/[.!?]$/.test(t) ? t : t + ".");
/** prompt is the ready-to-paste text, from the entry's problem and solution. */
export function prompt(eventTitle: string, teamName: string, entry?: { problem?: string; tagline?: string } | null): string {
  const lines = ["Build a one-page website for our hackathon project and publish it as our team site for " + (eventTitle || "our hackathon") + " (team " + (teamName || "") + ") on Simple Hack."];
  const problem = String(entry?.problem || "").trim();
  const solution = String(entry?.tagline || "").trim();
  if (problem) lines.push("Problem: " + end(problem));
  if (solution) lines.push("Solution: " + end(solution));
  return lines.join("\n");
}

/** archiveError is a refused zip in plain words (the extractor's own text is for us, not for a team). */
export function archiveError(err: unknown, fallback: string): string {
  const code = err instanceof ApiError ? err.code : "";
  if (code === "missing_index") return "Put index.html at the top of the zip, not inside a folder.";
  if (code === "invalid_archive") {
    const raw = String((err as ApiError).body?.error || (err as ApiError).message || "");
    if (/unsafe path|duplicate (entry|path)/i.test(raw))
      return "That zip has file paths Simple Host can't accept (a path outside the archive, or two files at the same path). Fix the archive and try again.";
    return "That file isn't a valid .zip.";
  }
  return say(err, fallback);
}

// Webhooks (a team site's notifications).
export type Hook = {
  id: string;
  source?: "owner" | "mcp" | string;
  app_name?: string;
  label?: string;
  url?: string;
  events?: string[];
  storage?: string;
  state?: string;
  last_attempt_at?: string | null;
  last_result?: string;
};
export type HookList = { notifications?: Hook[]; limits?: { enabled?: boolean; can_manage?: boolean; can_create?: boolean } };
export type Delivery = { name: string; created_at: string; state: string; result?: string; last_http?: number; attempts?: number };

export const NOTIFY_EVENTS = ["record.added", "record.changed", "file.uploaded"];
export const EVENT_LABEL: Record<string, string> = { "record.added": "new entries", "record.changed": "changed or removed entries", "file.uploaded": "uploaded files", "notification.test": "test notification" };
export const STATE_LABEL: Record<string, string> = { active: "Active", paused: "Paused", suspended: "Suspended after failures", pending: "Waiting for check", expired: "Expired" };
export const DELIVERY_LABEL: Record<string, string> = { pending: "Waiting for delivery", delivered: "Delivered", failed: "Failed", skipped: "Skipped" };
export const RESULT_LABEL: Record<string, string> = {
  secret_unreadable: "Stored secret cannot be read with this server's key; rotate it or add the notification again.",
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

export function receiver(n: Hook): string {
  if (n.source === "mcp") return n.app_name || "Connected app";
  if (n.label) return n.label;
  try {
    return new URL(n.url || "").host;
  } catch {
    return n.url || "";
  }
}

export function hookLine(n: Hook): string {
  const sub = (n.events || []).map((e) => EVENT_LABEL[e] || e).join(", ") + " · " + (n.storage || "all saved data") + " · " + (STATE_LABEL[n.state || ""] || n.state || "");
  const last = n.last_attempt_at ? (RESULT_LABEL[n.last_result || ""] || "No result yet") + " · " + dateTime(n.last_attempt_at) : "No deliveries yet";
  return sub + " · " + last;
}

export function deliveryLine(v: Delivery): string {
  const state = (DELIVERY_LABEL[v.state] || v.state) + (v.result ? " · " + (RESULT_LABEL[v.result] || v.result) : "") + (v.last_http ? " (HTTP " + v.last_http + ")" : "");
  return dateTime(v.created_at) + " · " + state + " · " + (v.attempts || 0) + " attempt" + (v.attempts === 1 ? "" : "s");
}

export function testLine(r: { delivered?: boolean; result?: string; status?: number }): string {
  return (r.delivered ? "Test delivered" : (RESULT_LABEL[r.result || ""] || "No result yet").replace(/\.$/, "")) + (r.status ? " (HTTP " + r.status + ")" : "") + ".";
}

// The connect steps (hack-connect-steps.json, shared with /get-started).
export type ConnectStep = { html: string; after?: string; address?: boolean; command?: string; copy?: { label: string; value: string } };
export type App = { id: string; label: string; tab?: boolean; connect?: ConnectStep[]; connect_hosted?: ConnectStep[]; fallback?: string };
export type Steps = { apps?: App[]; publish?: { html: string; prompt?: boolean }[]; signin_hint?: string; signin_hint_username?: string };

const esc = (s: string) => s.replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]!);
/** fill puts the app's name and the person's sign-in into a step's trusted markup (the person's own words escaped). */
export const fill = (t: string, app: string, email: string) =>
  String(t || "")
    .split("{app}")
    .join(esc(app))
    .split("{email}")
    .join("<b>" + esc(email || "your email") + "</b>")
    .split("{username}")
    .join("<b>" + esc(email) + "</b>");
