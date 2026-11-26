// Signing in, exactly as the old app does it (products/hack/webassets/static/hack/app.js):
// the account's API key lives in this browser's localStorage ("apiKey") and goes
// out as X-API-Key; POST /v1/sso/session binds the browser's SSO cookie to it so
// team and event sites sign the person in too. Sign-in is its own screen
// (/signin, screens/public/SignIn.tsx); this app sends people there and back with ?next.

const KEY = "apiKey";
const NEXT_KEY = "sh-hack-next";

export function apiKey(): string {
  try {
    return localStorage.getItem(KEY) || "";
  } catch {
    return "";
  }
}

/** startSSO refreshes this browser's SSO cookie from the key (once per page load, as the old app does). */
export function startSSO(): void {
  const key = apiKey();
  if (!key) return;
  fetch("/v1/sso/session", { method: "POST", headers: { "X-API-Key": key, "Content-Type": "application/json" } }).catch(() => {});
}

/** joinPath is a link page that works signed out (join, judge, organiser and team links, apply). */
export function joinPath(path: string): boolean {
  const p = path.replace(/\/+$/, "");
  return /^\/(join|judge|organiser|team)\/[^/]+$/.test(p) || /^\/e\/[^/]+\/apply$/.test(p);
}

/** safeNext is a same-origin path to come back to after signing in, or "". */
export function safeNext(n: string): string {
  if (!n) return "";
  try {
    const u = new URL(n, location.origin);
    if (u.origin !== location.origin) return "";
    for (const k of ["from", "utm_source", "utm_medium", "utm_campaign", "utm_term", "utm_content", "referrer"]) u.searchParams.delete(k);
    const path = u.pathname + u.search + u.hash;
    if (new URL(decodeURIComponent(path), location.origin).origin !== location.origin) return "";
    return path;
  } catch {
    return "";
  }
}

type Arrival = { url: (u: string) => string };
const arrival = (u: string) => ((window as unknown as { SHArrival?: Arrival }).SHArrival?.url(u) ?? u);

/** goSignIn leaves for the sign-in page, which comes back here afterwards. */
export function goSignIn(): void {
  if (location.pathname === "/signin") return;
  const next = safeNext(location.pathname + location.search + location.hash) || "/events";
  try {
    sessionStorage.setItem(NEXT_KEY, next);
  } catch {
    /* storage blocked */
  }
  try {
    localStorage.setItem(NEXT_KEY, JSON.stringify({ path: next, exp: Date.now() + 30 * 60 * 1000 }));
  } catch {
    /* storage blocked */
  }
  location.assign(arrival("/signin?next=" + encodeURIComponent(next)));
}

/** forget drops what this browser keeps for the signed-in person: the key and the assistant's chats. */
export function forget(): void {
  for (const k of [KEY, "sh-email-nonce"])
    try {
      localStorage.removeItem(k);
    } catch {
      /* storage blocked */
    }
  for (const k of ["sh-google-nonce", "sh-connect-nonce"])
    try {
      sessionStorage.removeItem(k);
    } catch {
      /* storage blocked */
    }
  try {
    const stale: string[] = [];
    for (let i = 0; i < localStorage.length; i++) {
      const k = localStorage.key(i);
      if (k?.startsWith("sh-assistant-")) stale.push(k);
    }
    stale.forEach((k) => localStorage.removeItem(k));
  } catch {
    /* storage blocked */
  }
}

/** signedOut is what a refused key does: forget it, then sign in again (a link page stays put). */
export function signedOut(): void {
  forget();
  if (joinPath(location.pathname)) return;
  goSignIn();
}

/** The toast the old app leaves for Your events after leaving an event (leave.js takeFlash). */
const FLASH_KEY = "hack-flash";
export function flash(text: string): void {
  try {
    sessionStorage.setItem(FLASH_KEY, text);
  } catch {
    /* storage blocked */
  }
}
export function takeFlash(): string {
  try {
    const t = sessionStorage.getItem(FLASH_KEY) || "";
    sessionStorage.removeItem(FLASH_KEY);
    return t;
  } catch {
    return "";
  }
}
