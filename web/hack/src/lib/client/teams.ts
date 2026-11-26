// Teams, as the organiser manages any team.
import type { Call } from "@kit/api/call";
import { E, seg, type Untyped } from "./_e";

export const list = (call: Call, slug: string) => call<Untyped>("GET", E(slug, "teams"));
export const create = (call: Call, slug: string, body: Untyped) => call<Untyped>("POST", E(slug, "teams"), body);
export const open = (call: Call, slug: string) => call<Untyped>("GET", E(slug, "teams", "open"));
export const fill = (call: Call, slug: string, body: Untyped) => call<Untyped>("POST", E(slug, "teams", "fill"), body);
export const update = (call: Call, slug: string, team: string, body: Untyped) => call<Untyped>("PATCH", E(slug, "teams", seg(team)), body);
export const remove = (call: Call, slug: string, team: string) => call<Untyped>("DELETE", E(slug, "teams", seg(team)));
export const addMember = (call: Call, slug: string, team: string, body: Untyped) => call<Untyped>("POST", E(slug, "teams", seg(team), "members"), body);
export const removeMember = (call: Call, slug: string, team: string, user: string) => call<Untyped>("DELETE", E(slug, "teams", seg(team), "members", seg(user)));
export const setAdmin = (call: Call, slug: string, team: string, user: string, body: Untyped) => call<Untyped>("PUT", E(slug, "teams", seg(team), "members", seg(user), "admin"), body);
export const lock = (call: Call, slug: string, team: string, body: Untyped) => call<Untyped>("PUT", E(slug, "teams", seg(team), "lock"), body);
export const newLink = (call: Call, slug: string, team: string) => call<Untyped>("POST", E(slug, "teams", seg(team), "link"));
export const deadline = (call: Call, slug: string, team: string, body: Untyped) => call<Untyped>("PUT", E(slug, "teams", seg(team), "deadline"), body);
export const takeDown = (call: Call, slug: string, team: string, body: Untyped = {}) => call<Untyped>("POST", E(slug, "teams", seg(team), "takedown"), body);
export const restore = (call: Call, slug: string, team: string) => call<Untyped>("POST", E(slug, "teams", seg(team), "restore"));
