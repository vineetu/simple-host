// Webhooks, the same shape twice: the event site's (E/website/notifications)
// and a team site's (/v1/sites/{team}/notifications?event=…). base() picks which.
import type { Call } from "@kit/api/call";
import { E, seg, type Untyped } from "./_e";

export type Hooks = { base: string; q: string };
export const eventSite = (slug: string): Hooks => ({ base: E(slug, "website", "notifications"), q: "" });
export const teamSite = (team: string, slug: string): Hooks => ({ base: "/v1/sites/" + seg(team) + "/notifications", q: "?event=" + seg(slug) });

export const list = (call: Call, h: Hooks) => call<Untyped>("GET", h.base + h.q);
export const create = (call: Call, h: Hooks, body: Untyped) => call<Untyped>("POST", h.base + h.q, body);
export const update = (call: Call, h: Hooks, id: string, body: Untyped) => call<Untyped>("PATCH", h.base + "/" + seg(id) + h.q, body);
export const remove = (call: Call, h: Hooks, id: string) => call<Untyped>("DELETE", h.base + "/" + seg(id) + h.q);
export const deliveries = (call: Call, h: Hooks, id: string) => call<Untyped>("GET", h.base + "/" + seg(id) + "/deliveries" + h.q);
export const test = (call: Call, h: Hooks, id: string) => call<Untyped>("POST", h.base + "/" + seg(id) + "/test" + h.q);
export const rotateSecret = (call: Call, h: Hooks, id: string) => call<Untyped>("POST", h.base + "/" + seg(id) + "/rotate-secret" + h.q);
