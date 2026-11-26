// Sign-in's plain logic, exactly the old app's (static/hack/app.js routeSignIn
// and the platform card static/signin.js): where to go afterwards (?next,
// kept in both storages for 30 minutes), the single-use nonces that tie an
// emailed link or a Google / company return to this browser, which methods
// the server offers, and the one sentence for each refusal.
import { ApiError } from "@kit/api/call";
import { safeNext } from "@/lib/session";

const NEXT_KEY = "sh-hack-next";

function saveNext(path: string) {
  try {
    sessionStorage.setItem(NEXT_KEY, path);
  } catch {
    /* storage blocked */
  }
  try {
    localStorage.setItem(NEXT_KEY, JSON.stringify({ path, exp: Date.now() + 30 * 60 * 1000 }));
  } catch {
    /* storage blocked */
  }
}

function storedNext(): string {
  let n = "";
  try {
    n = sessionStorage.getItem(NEXT_KEY) || "";
  } catch {
    /* storage blocked */
  }
  const p = safeNext(n);
  if (p) return p;
  try {
    const raw = localStorage.getItem(NEXT_KEY);
    if (!raw) return "";
    const o = JSON.parse(raw) as { path?: string; exp?: number } | null;
    if (!o || !((o.exp || 0) > Date.now())) {
      localStorage.removeItem(NEXT_KEY);
      return "";
    }
    return safeNext(o.path || "");
  } catch {
    return "";
  }
}

/** readNext is ?next (or the stored one), stored again so a Google return still has it. */
export function readNext(search: string = location.search): string {
  const n = safeNext(new URLSearchParams(search).get("next") || "") || storedNext();
  if (n) saveNext(n);
  return n;
}

/** consumeNext is readNext, then forgets it (it is used once). */
export function consumeNext(search: string = location.search): string {
  const n = readNext(search);
  try {
    sessionStorage.removeItem(NEXT_KEY);
  } catch {
    /* storage blocked */
  }
  try {
    localStorage.removeItem(NEXT_KEY);
  } catch {
    /* storage blocked */
  }
  return n;
}

// Nonces: the email one lives 15 minutes in localStorage (the link may open
// in another tab), Google's and the company account's in this tab's sessionStorage.
export const EMAIL_NONCE = "sh-email-nonce";
export const GOOGLE_NONCE = "sh-google-nonce";
export const OIDC_NONCE = "sh-oidc-nonce";

function b64url(bytes: Uint8Array): string {
  let s = "";
  bytes.forEach((b) => (s += String.fromCharCode(b)));
  return btoa(s).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}
export function newNonce(): string {
  const r = new Uint8Array(32);
  crypto.getRandomValues(r);
  return b64url(r);
}
export async function nonceHash(n: string): Promise<string> {
  return b64url(new Uint8Array(await crypto.subtle.digest("SHA-256", new TextEncoder().encode(n))));
}

/** emailNonceHash stores a fresh email nonce and returns its hash, or "" when storage is blocked. */
export async function emailNonceHash(): Promise<string> {
  const n = newNonce();
  try {
    localStorage.setItem(EMAIL_NONCE, JSON.stringify({ n, exp: Date.now() + 15 * 60 * 1000 }));
  } catch {
    return "";
  }
  return nonceHash(n);
}

/** providerNonce stores a fresh Google or company nonce for this tab; null when storage is blocked. */
export function providerNonce(provider: string): string | null {
  const n = newNonce();
  try {
    sessionStorage.setItem(provider === "google" ? GOOGLE_NONCE : OIDC_NONCE, n);
  } catch {
    return null;
  }
  return n;
}

/** takeNonceFor is the stored nonce (email, Google or company) that hashes to cn, or "". A match is used up. */
export async function takeNonceFor(cn: string | null): Promise<string> {
  const candidates: string[] = [];
  try {
    const g = sessionStorage.getItem(GOOGLE_NONCE);
    if (g) candidates.push(g);
    const o = sessionStorage.getItem(OIDC_NONCE);
    if (o) candidates.push(o);
    const e = JSON.parse(localStorage.getItem(EMAIL_NONCE) || "null") as { n?: string; exp?: number } | null;
    if (e && e.n && (e.exp || 0) > Date.now()) candidates.push(e.n);
  } catch {
    /* storage blocked */
  }
  if (!cn) return "";
  for (const n of candidates) {
    if ((await nonceHash(n)) !== cn) continue;
    try {
      sessionStorage.removeItem(GOOGLE_NONCE);
      sessionStorage.removeItem(OIDC_NONCE);
    } catch {
      /* storage blocked */
    }
    try {
      const e = JSON.parse(localStorage.getItem(EMAIL_NONCE) || "null") as { n?: string } | null;
      if (e && e.n === n) localStorage.removeItem(EMAIL_NONCE);
    } catch {
      /* storage blocked */
    }
    return n;
  }
  return "";
}

/** What GET /v1/auth/oauth/providers offers, in the card's terms. */
export type Methods = { providers: ("google" | "oidc")[]; emailOn: boolean; oidcLabel: string; password?: { enabled?: boolean; self_signup?: boolean } };

export function methods(d: { providers?: string[]; email_enabled?: boolean; oidc_label?: string; password?: Methods["password"] } | null): Methods {
  if (!d) return { providers: [], emailOn: true, oidcLabel: "" };
  const names = d.providers || [];
  const providers: Methods["providers"] = [];
  if (names.includes("google")) providers.push("google");
  if (names.includes("oidc")) providers.push("oidc");
  return { providers, emailOn: d.email_enabled !== false, oidcLabel: d.oidc_label || "", password: d.password };
}

export const anyMethod = (m: Methods) => m.emailOn || m.providers.length > 0 || !!m.password?.enabled;

export const IT_FINE = "Email codes are turned off on this server. Ask your IT team if you cannot sign in.";

/** companyCopy is the card's lead and small print on a company install (no email codes, no passwords). */
export function companyCopy(m: Methods): { lead?: string; fine: string } | null {
  if (m.emailOn || !m.providers.length || m.password?.enabled) return null;
  const oidc = m.providers.includes("oidc");
  const google = m.providers.includes("google");
  return { fine: IT_FINE, ...(oidc && !google ? { lead: "Use your " + (m.oidcLabel || "company") + " account." } : {}) };
}

const EMAIL_RE = /^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$/;
export const validEmail = (s: string) => EMAIL_RE.test(s.trim());
export const digits = (s: string) => s.replace(/\D/g, "").slice(0, 6);
export function isGmail(email: string): boolean {
  const e = email.trim().toLowerCase();
  const d = e.slice(e.lastIndexOf("@") + 1);
  return e.includes("@") && (d === "gmail.com" || d === "googlemail.com");
}
export const gmailURL = (ua = navigator.userAgent) =>
  /Android/i.test(ua) ? "intent://#Intent;scheme=googlegmail;package=com.google.android.gm;S.browser_fallback_url=https%3A%2F%2Fmail.google.com%2Fmail%2F;end" : "https://mail.google.com/mail/";

// The server's sign-in sentences (host/web/signinmsg.go SignInMessages, which
// the old card gets injected); {product} is Simple Hack.
const MESSAGES: Record<string, string> = {
  bad_credentials: "Wrong username or password.",
  password_change_required: "Choose a new password to finish signing in.",
  username_taken: "That username is taken.",
  code_wrong: "That code is not right. {n} tries left.",
  code_used_up: "Too many wrong tries, so this code no longer works. Send a new one to try again.",
  code_expired: "That code has expired or was already used. Codes last {min} minutes. Send a new code.",
  rate_limited: "Too many tries. Wait a minute, then try again.",
  invalid_email: "That email address does not look right.",
  send_failed: "We could not send the email just now. Try again in a minute.",
  signup_blocked: "Sign-in isn't available from your region.",
  email_signin_off: "Email sign-in is off on this server. Sign in with your company account.",
  email_unavailable: "This server cannot send sign-in codes. Use Google if it is offered, or ask whoever runs this server.",
  link_other_browser: "Open this link on the device where you asked for it, or type the 6-digit code from the email.",
  account_suspended: "this account has been suspended by the operator",
  network: "Could not reach Simple Hack. Check your connection.",
  unknown: "Something went wrong. Try again.",
};

const fill = (tpl: string, vars: Record<string, unknown>) => {
  let out = tpl;
  for (const [k, v] of Object.entries(vars)) out = out.split("{" + k + "}").join(v == null ? "" : String(v));
  return out.replace(/\s+/g, " ").replace(/\s+\./g, ".").trim();
};

/** message is the one sentence for a sign-in refusal (signin.js message()). */
export function message(err: unknown): string {
  const e = (err || {}) as Partial<ApiError> & { message?: string };
  const body = (e.body || {}) as Record<string, unknown>;
  const reason = String(body.reason || "");
  const code = String(e.code || body.code || "");
  if (code === "account_suspended") return String(body.error || e.message || MESSAGES.account_suspended);
  let key = MESSAGES[reason] ? reason : "";
  if (!key) {
    if (code === "signup_unavailable_region" || code === "signup_blocked") key = "signup_blocked";
    else if (code === "invalid_code") key = "code_expired";
    else if (MESSAGES[code]) key = code;
    else if (e.status === 0 || code === "network" || /network|failed to fetch|load failed/i.test(String(e.message || ""))) key = "network";
    else key = "unknown";
  }
  if (key === "account_suspended") return String(body.error || e.message || MESSAGES.account_suspended);
  if (code === "invalid_password" || code === "invalid_username") return String(body.error || e.message);
  const n = body.tries_left;
  if (key === "code_wrong") {
    if (n == 1) return "That code is not right. 1 try left.";
    if (n != null && n !== "") return fill(MESSAGES.code_wrong, { n });
    return "That code is not right.";
  }
  if (key === "code_expired" && body.min == null) return "That code has expired or was already used. Send a new code.";
  if (body.error && !reason && (key === "unknown" || key === "code_expired")) return String(body.error);
  return fill(MESSAGES[key], { n, min: body.min });
}

/** reasonOf is a code refusal's reason (code_used_up and code_expired change the card's main button). */
export const reasonOf = (err: unknown) => String(((err as ApiError)?.body || {}).reason || "");

/** landing is where a signed-in person goes with no ?next: their one current event, else Your events. */
export function landing(events: { slug: string; role: string; stage: string; taken_down?: boolean }[], home: (role: string, slug: string) => string): string {
  const current = events.filter((e) => e.stage !== "archived" && !e.taken_down);
  return current.length === 1 ? home(current[0].role, current[0].slug) : "/events";
}
