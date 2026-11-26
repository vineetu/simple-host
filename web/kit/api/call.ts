// The base every app's API client is built on: one way to call the service
// (Call), one refusal type (ApiError) and the React context that hands the
// Call to screens. Each app supplies its own fetch adapter (how it signs
// requests and reads refusals); the catalogue swaps in fixtures.
// (Simple Host's owner app still has its own copy in web/host/src/lib/api.ts,
// which keeps its built output byte-identical; it converges here when it next changes.)
import { createContext, useContext } from "react";

/** ApiError is a refusal from the service: its status, its code and the one line to show. */
export class ApiError extends Error {
  status: number;
  code: string;
  /** The refusal's whole body, for screens that read more than the message. */
  body: Record<string, unknown>;
  constructor(status: number, code: string, message: string, body: Record<string, unknown> = {}) {
    super(message);
    this.status = status;
    this.code = code;
    this.body = body;
  }
}

/** Call is how screens talk to the /v1 routes. */
export type Call = <T>(method: string, path: string, body?: unknown) => Promise<T>;

const ApiContext = createContext<Call | null>(null);
export const ApiProvider = ApiContext.Provider;

/** useApi is the Call of the nearest ApiProvider. */
export function useApi(): Call {
  const call = useContext(ApiContext);
  if (!call) throw new Error("useApi needs an ApiProvider");
  return call;
}

/** Bound turns an area module (functions taking a Call first) into the same functions with the Call filled in.
 *  Anything else the module exports (a path helper, a type) is left out; import it from the module. */
export type Bound<M> = {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  [K in keyof M as M[K] extends (call: Call, ...args: any[]) => unknown ? K : never]: M[K] extends (call: Call, ...args: infer A) => infer R ? (...args: A) => R : never;
};

/** bind fills in the Call of every function of an area module (Bound hides the ones that take no Call). */
export function bind<M extends Record<string, unknown>>(area: M, call: Call): Bound<M> {
  const out: Record<string, unknown> = {};
  for (const [name, fn] of Object.entries(area)) if (typeof fn === "function") out[name] = (...args: unknown[]) => fn(call, ...args);
  return out as Bound<M>;
}
