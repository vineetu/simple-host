// Operator (platform admin): Hack events on this install.
import type { Call } from "@kit/api/call";
import { seg, type Untyped } from "./_e";
const A = (slug: string, act = "") => "/v1/admin/hack/events/" + seg(slug) + (act ? "/" + act : "");
export const events = (call: Call) => call<Untyped>("GET", "/v1/admin/hack/events");
export const act = (call: Call, slug: string, what: "takedown" | "keep-sites" | "restore", body: Untyped = {}) => call<Untyped>("POST", A(slug, what), body);
export const remove = (call: Call, slug: string) => call<Untyped>("DELETE", A(slug));
