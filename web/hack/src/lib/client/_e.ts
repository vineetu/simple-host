// Path helpers shared by the area files: E(slug, ...) is /v1/hack/events/{slug}/...
import { path } from "@kit/api/http";
export { query } from "@kit/api/http";
export const E = (slug: string, ...rest: string[]) => path("/v1/hack/events", slug) + rest.map((r) => "/" + r).join("");
export const seg = (s: string) => encodeURIComponent(s);
/** A payload not typed yet: the screen that is rebuilt to use it types it in this file. */
export type Untyped = Record<string, unknown>;
