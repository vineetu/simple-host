// Small helpers for building /v1 paths.

/** path joins segments, escaping each one, after a fixed prefix: path("/v1/hack/events", slug, "leave"). */
export function path(prefix: string, ...segments: (string | number)[]): string {
  return prefix + segments.map((s) => "/" + encodeURIComponent(String(s))).join("");
}

/** query is "?a=1&b=2" for the set values, or "" when none is set. */
export function query(params: Record<string, string | number | boolean | undefined | null>): string {
  const q = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) if (v !== undefined && v !== null && v !== "") q.set(k, String(v));
  const s = q.toString();
  return s ? "?" + s : "";
}
