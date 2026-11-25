import { createContext, useContext } from "react";

/** ApiError is a refusal from the service: its code and the one-line fix it gave. */
export class ApiError extends Error {
  status: number;
  code: string;
  constructor(status: number, code: string, fix: string) {
    super(fix);
    this.status = status;
    this.code = code;
  }
}

/** Call is how screens talk to the /v1 routes. The catalogue swaps in fixtures. */
export type Call = <T>(method: string, path: string, body?: unknown) => Promise<T>;

export const fetchCall: Call = async <T,>(method: string, path: string, body?: unknown): Promise<T> => {
  const headers: Record<string, string> = { "X-SH-CSRF": "1" };
  if (body !== undefined) headers["Content-Type"] = "application/json";
  let res: Response;
  try {
    res = await fetch(path, {
      method,
      credentials: "same-origin",
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  } catch {
    throw new ApiError(0, "offline", "Cannot reach Simple Host. Check your connection and try again.");
  }
  const answer = await res.json().catch(() => ({}));
  if (!res.ok) throw new ApiError(res.status, answer.code || "error", answer.fix || "That did not work. Try again.");
  return answer as T;
};

const ApiContext = createContext<Call>(fetchCall);
export const ApiProvider = ApiContext.Provider;
export const useApi = () => useContext(ApiContext);

/** googleOn is whether this install offers Google sign-in (the server fills the meta tag). */
export function googleOn(): boolean {
  return document.querySelector('meta[name="sh-google"]')?.getAttribute("content") === "1";
}
