// Simple Hack's fetch adapter for the kit's Call: the key from this browser
// (X-API-Key), refusals read from {error, code}, a 401 signs the person out.
// Bytes (screenshots, downloads) go through bytes(), which signs the same way.
import { ApiError, type Call } from "@kit/api/call";
import { apiKey, signedOut } from "./session";

const OFFLINE = "Cannot reach the server.";

function refusal(status: number, data: Record<string, unknown>): ApiError {
  const code = typeof data.code === "string" && data.code ? data.code : "error";
  const msg = status >= 500 ? "Something went wrong. Try again." : typeof data.error === "string" && data.error ? data.error : "Something went wrong.";
  return new ApiError(status, code, msg, data);
}

// Calls that add an event to the person's list or take one off it; the app
// re-reads the list after any of them (api.js MEMBERSHIP_MUTATIONS).
const MEMBERSHIP: [string, RegExp][] = [
  ["POST", /^\/v1\/hack\/events$/],
  ["POST", /^\/v1\/hack\/(organiser|join|judge)\/[^/]+$/],
  ["DELETE", /^\/v1\/hack\/events\/[^/]+$/],
  ["POST", /^\/v1\/hack\/events\/[^/]+\/(stage|leave)$/],
  ["DELETE", /^\/v1\/hack\/events\/[^/]+\/(people|organisers)\/[^/]+$/],
];
export const changesMembership = (method: string, path: string) => MEMBERSHIP.some(([m, re]) => m === method && re.test(path.split("?")[0]));

function headers(json: boolean): Record<string, string> {
  const h: Record<string, string> = {};
  if (json) h["Content-Type"] = "application/json";
  const k = apiKey();
  if (k) h["X-API-Key"] = k;
  return h;
}

/** hackCall makes a Call; onMembership runs after a call that changed which events the person is in. */
export function hackCall(onMembership: () => void = () => {}): Call {
  return async <T,>(method: string, path: string, body?: unknown): Promise<T> => {
    let res: Response;
    try {
      res = await fetch(path, { method, headers: headers(true), body: body === undefined ? undefined : JSON.stringify(body) });
    } catch {
      throw new ApiError(0, "offline", OFFLINE);
    }
    const data = (await res.json().catch(() => ({}))) as Record<string, unknown>;
    if (res.status === 401) {
      signedOut();
      throw new ApiError(401, "signed_out", "You are signed out.");
    }
    if (!res.ok) throw refusal(res.status, data);
    if (changesMembership(method, path)) onMembership();
    return data as T;
  };
}

/** Bytes fetches a file the key may read (a screenshot, an export) as a Blob. */
export type Bytes = (path: string) => Promise<Blob>;

export const fetchBytes: Bytes = async (path) => {
  let res: Response;
  try {
    res = await fetch(path, { headers: headers(false) });
  } catch {
    throw new ApiError(0, "offline", OFFLINE);
  }
  if (res.status === 401) {
    signedOut();
    throw new ApiError(401, "signed_out", "You are signed out.");
  }
  if (!res.ok) throw refusal(res.status, (await res.json().catch(() => ({}))) as Record<string, unknown>);
  return res.blob();
};
