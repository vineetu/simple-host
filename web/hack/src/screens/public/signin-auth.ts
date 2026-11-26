// How the sign-in screen talks to /v1/auth: plain requests with no key (a
// stale key in this browser must not ride along, and a 401 here is a wrong
// code or password, not a signed-out session), exactly as the old app's
// routeSignIn did. The catalogue's fixtures stand in through its Call.
import { useMemo } from "react";
import { ApiError, useApi, type Call } from "@kit/api/call";
import * as auth from "@/lib/client/auth";

type Arrival = { url: (u: string) => string };
/** arrival carries the visit's attribution onto a URL (the server's /arrival.js), when it is loaded. */
export const arrival = (u: string) => ((window as unknown as { SHArrival?: Arrival }).SHArrival?.url(u) ?? u);

/** plain is a Call with no key and no signed-out handling; a refusal keeps its whole body. */
export const plain: Call = async <T,>(method: string, path: string, body?: unknown): Promise<T> => {
  let res: Response;
  try {
    res = await fetch(path, { method, headers: { "Content-Type": "application/json" }, body: body === undefined ? undefined : JSON.stringify(body) });
  } catch {
    throw new ApiError(0, "network", "Failed to fetch");
  }
  const data = (await res.json().catch(() => ({}))) as Record<string, unknown>;
  if (!res.ok) throw new ApiError(res.status, typeof data.code === "string" ? data.code : "", typeof data.error === "string" ? data.error : "error", data);
  return data as T;
};

/** ssoWith binds this browser's SSO cookie to a new key (team and event sites sign in from it). */
async function ssoWith(key: string): Promise<void> {
  const r = await fetch("/v1/sso/session", { method: "POST", headers: { "X-API-Key": key, "Content-Type": "application/json" } });
  if (!r.ok) throw new Error("Sign-in unavailable. Try again.");
}

const inCatalogue = () => /\/catalogue\.html$/.test(location.pathname);

/** useAuth is the sign-in calls, on plain requests (or the catalogue's fixtures). */
export function useAuth() {
  const fixtures = useApi();
  return useMemo(() => {
    const call = inCatalogue() ? fixtures : plain;
    return {
      providers: () => auth.providers(call),
      requestCode: (email: string, nonce_hash: string) => auth.requestCode(call, email, nonce_hash || undefined),
      // Verify goes through arrival, as the old app sent it (auth.verify's path otherwise).
      verify: (body: Record<string, unknown>) => call<{ api_key: string }>("POST", arrival("/v1/auth/verify"), body),
      password: (body: Record<string, unknown>) => auth.password(call, body),
      passwordSignup: (body: Record<string, unknown>) => auth.passwordSignup(call, body),
      sso: (key: string) => (inCatalogue() ? fixtures("POST", "/v1/sso/session").then(() => undefined) : ssoWith(key)),
    };
  }, [fixtures]);
}
