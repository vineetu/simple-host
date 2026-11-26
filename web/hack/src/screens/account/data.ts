// Account and Create event: the plain logic (account.js, connect-steps.js,
// organiser-create-event.js, _orgA-util.js), kept free of React for the tests.
import type { ApiError } from "@kit/api/call";
import type { Account, Connection, Identity } from "@/lib/client/me";
import type { ConnectApp, ConnectStep } from "@/lib/client/hack";

/** site is where this app runs. The catalogue pins it so its pictures do not depend on the port it is served from. */
export const site = {
  origin: () => location.origin,
  /** The host event addresses live under (location.host: a self-hosted install's own domain). */
  host: () => location.host,
};

/** The words the old screens show for the codes these two screens can meet (ui.js ERROR_MESSAGES). */
export const CODES: Record<string, string> = {
  invalid_name: "Use 3 to 39 lowercase letters, numbers or hyphens. It can't start or end with a hyphen.",
  name_reserved: "That name is reserved.",
  name_taken: "That name is already in use.",
  name_check_unavailable: "Could not check whether this name is free. Try again.",
  rate_limited: "Too many changes at once. Wait a moment and try again.",
  invalid_display_name: "Enter your name on one line, without special formatting.",
  invalid_request: "Something in that request wasn't right. Reload and try again.",
};

/** errText is the line to show for a refusal: a known code's words, else the server's own sentence. */
export function errText(e: unknown, fallback = "Something went wrong."): string {
  const err = e as Partial<ApiError>;
  if (err?.code && CODES[err.code]) return CODES[err.code];
  return err?.message || fallback;
}

/** Words is how the account is named on this screen (account.js paintAccount). */
export type Words = {
  /** The account's identifying word: what Delete asks to type, and what Google linking names. */
  email: string;
  /** What DELETE /v1/me is sent as confirm (the handle when there is one). */
  confirm: string;
  /** A real address to show on the Email part, or "". */
  realEmail: string;
  login: string;
};

export function words(me: Account): Words {
  const email = String(me.login || me.email || me.username || "");
  const username = String(me.username || "");
  return {
    email,
    confirm: me.handle ? String(me.handle) : email,
    realEmail: String(me.email || (username.includes("@") ? username : "")),
    login: String(me.login || ""),
  };
}

/** canOpen is false for a team key, the platform admin, or a key with no person behind it: they see a note instead. */
export const canOpen = (me: Account) => !(me.team || me.username === "admin" || !(me.username || me.email));

/** accountErr is a refusal's sentence with the account's internal names taken out (account.js accountErr). */
export function accountErr(e: unknown, w: Words | null, fallback = "Something went wrong"): string {
  const err = e as Partial<ApiError>;
  let msg = (err?.status ?? 0) > 0 && err?.message ? err.message : err?.status === 0 ? "Cannot reach the server." : fallback;
  if (w && w.confirm && w.confirm !== w.email && msg.includes(w.confirm)) msg = msg.split(w.confirm).join("your account");
  return msg.replace(/\bu-[0-9a-f]{4,}\b/gi, "your account");
}

/** ago is "just now", "5 minutes ago", "2 hours ago", "3 days ago" (account.js connectAgoText). */
export function ago(iso: string | undefined, now = Date.now()): string {
  if (!iso) return "";
  const diff = now - new Date(iso).getTime();
  if (!Number.isFinite(diff)) return "";
  if (diff < 60000) return "just now";
  const m = Math.floor(diff / 60000);
  if (m < 60) return m + " minute" + (m === 1 ? "" : "s") + " ago";
  const h = Math.floor(m / 60);
  if (h < 24) return h + " hour" + (h === 1 ? "" : "s") + " ago";
  const d = Math.floor(h / 24);
  return d + " day" + (d === 1 ? "" : "s") + " ago";
}

/** providerName is who a linked sign-in is from. */
export const providerName = (i: Identity, oidcLabel: string) =>
  i.provider === "github" ? "GitHub" : i.provider === "oidc" ? oidcLabel || "your company account" : "Google";

/** identityLine is a linked sign-in's sentence. */
export function identityLine(i: Identity, who: string, linked: string): string {
  let line = who + " sign-in: " + (i.email || "no email shared");
  if (linked) line += ", linked " + linked;
  return line + ". This " + who + " account can sign in to yours.";
}

/** connections sorts the AI apps most recently used first and names each (with its device when two share a name). */
export function connectionRows(list: Connection[], now = Date.now()): { c: Connection; name: string; line: string }[] {
  const at = (c: Connection) => new Date(c.last_used_at || c.connected_at || 0).getTime() || 0;
  const sorted = list.slice().sort((a, b) => at(b) - at(a));
  return sorted.map((c) => {
    const app = c.name || "an AI app";
    const name = c.device && sorted.filter((o) => (o.name || "an AI app") === app).length > 1 ? app + " (" + c.device + ")" : app;
    const teams = (c.teams || []).map((t) => t.team + " in " + t.event);
    const teamNames = teams.length < 2 ? teams[0] : teams.slice(0, -1).join(", ") + " and " + teams[teams.length - 1];
    const used = ago(c.last_used_at, now);
    return { c, name, line: (used ? "Last used " + used + " · " : "") + (teams.length ? "Publishes to " + teamNames : "Hasn't picked a team yet") };
  });
}

/** stepsFor is one app's connect steps: the hosted directory path on the hosted instance, with the by-address fallback after it (connect-steps.js). */
export function stepsFor(app: ConnectApp, origin: string): (ConnectStep & { fallback?: string })[] {
  const hosted = origin === "https://simple-hack.app";
  const steps: (ConnectStep & { fallback?: string })[] = ((hosted && app.connect_hosted) || app.connect || []).map((s) => ({ ...s }));
  if (hosted && app.fallback && steps.length) steps[steps.length - 1].fallback = app.fallback;
  return steps;
}

// ---- Google linking: the old app's sign-in mechanics, unchanged (app.js saveNext/startOAuthProvider) ----

const NEXT_KEY = "sh-hack-next";
const GOOGLE_NONCE = "sh-google-nonce";

function b64url(bytes: Uint8Array): string {
  let s = "";
  bytes.forEach((b) => (s += String.fromCharCode(b)));
  return btoa(s).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

export async function nonceHash(n: string): Promise<string> {
  return b64url(new Uint8Array(await crypto.subtle.digest("SHA-256", new TextEncoder().encode(n))));
}

type Arrival = { url: (u: string) => string };
const arrival = (u: string) => ((window as unknown as { SHArrival?: Arrival }).SHArrival?.url(u) ?? u);

/**
 * linkGoogle leaves for Google with /account as where to come back to. It is
 * signing in with Google: the server attaches the Google account to whichever
 * account its verified email matches. Returns false when the browser blocks
 * the storage the return trip needs (nothing happens then).
 */
export async function linkGoogle(oauthStart: (provider: string, returnTo: string) => string): Promise<boolean> {
  try {
    sessionStorage.setItem(NEXT_KEY, "/account");
  } catch {
    /* storage blocked */
  }
  try {
    localStorage.setItem(NEXT_KEY, JSON.stringify({ path: "/account", exp: Date.now() + 30 * 60 * 1000 }));
  } catch {
    /* storage blocked */
  }
  const r = new Uint8Array(32);
  crypto.getRandomValues(r);
  const n = b64url(r);
  try {
    sessionStorage.setItem(GOOGLE_NONCE, n);
  } catch {
    return false;
  }
  const hash = await nonceHash(n);
  location.assign(oauthStart("google", arrival(location.origin + "/?cn=" + hash)));
  return true;
}

// ---- Create event ----

/** slugify turns a title into an address: lowercase letters, numbers and single hyphens, at most 39. */
export function slugify(s: string, limit = 39): string {
  return String(s || "")
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, limit)
    .replace(/-+$/g, "");
}

export type CreateForm = {
  title: string;
  slug: string;
  start: string;
  end: string;
  tz: string;
  organiser: string;
  org: string;
  email: string;
  purpose: string;
  n: string;
};

const EMAIL = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

/** createErrors is what is wrong with the form, field by field, in plain words (checked as it is submitted). */
export function createErrors(f: CreateForm): Partial<Record<keyof CreateForm, string>> {
  const e: Partial<Record<keyof CreateForm, string>> = {};
  if (!f.title.trim()) e.title = "Give the event a title.";
  if (!f.slug.trim()) e.slug = "Choose an address.";
  if (!f.start) e.start = "Choose the day it starts.";
  else if (f.end && f.end < f.start) e.end = "It can't end before it starts.";
  if (!f.organiser.trim()) e.organiser = "Enter your name.";
  if (!f.email.trim()) e.email = "Enter an email people can contact you on.";
  else if (!EMAIL.test(f.email.trim())) e.email = "That email address does not look right.";
  if (!f.purpose.trim()) e.purpose = "Say in a line or two what the event is for.";
  const n = f.n.trim();
  if (!/^[1-9]\d*$/.test(n) || Number(n) > 100000) e.n = "Expected participants must be between 1 and 100000.";
  return e;
}

export const NAME_CODES = ["invalid_name", "name_reserved", "name_taken"];

