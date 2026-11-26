// A participant's own team.
import type { Call } from "@kit/api/call";
import { E, seg, type Untyped } from "./_e";

export const join = (call: Call, slug: string, body: Untyped) => call<Untyped>("POST", E(slug, "teams", "join"), body);
export const leave = (call: Call, slug: string) => call<Untyped>("POST", E(slug, "teams", "leave"));
export const update = (call: Call, slug: string, body: Untyped) => call<Untyped>("PATCH", E(slug, "team"), body);
export const lock = (call: Call, slug: string, body: Untyped) => call<Untyped>("PUT", E(slug, "team", "lock"), body);
export const newLink = (call: Call, slug: string) => call<Untyped>("POST", E(slug, "team", "link"));
export const invite = (call: Call, slug: string, body: Untyped) => call<Untyped>("POST", E(slug, "team", "invites"), body);
export const setAdmin = (call: Call, slug: string, user: string, body: Untyped) => call<Untyped>("PUT", E(slug, "team", "members", seg(user), "admin"), body);
export const removeMember = (call: Call, slug: string, user: string) => call<Untyped>("DELETE", E(slug, "team", "members", seg(user)));
export const askToJoin = (call: Call, slug: string, team: string, body: Untyped = {}) => call<Untyped>("POST", E(slug, "teams", seg(team), "requests"), body);
export const withdrawRequest = (call: Call, slug: string, id: string) => call<Untyped>("DELETE", E(slug, "requests", seg(id)));
export const answerRequest = (call: Call, slug: string, id: string, answer: "accept" | "decline") => call<Untyped>("POST", E(slug, "requests", seg(id), answer));
export const setLooking = (call: Call, slug: string, body: Untyped) => call<Untyped>("PUT", E(slug, "me", "looking"), body);
export const site = (call: Call, slug: string) => call<Untyped>("GET", E(slug, "site"));
/** The team's archive, fetched with bytes (the key must go with it). */
export const exportPath = (slug: string) => E(slug, "team", "export.tar.gz");
