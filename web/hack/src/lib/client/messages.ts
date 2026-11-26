// Announcements and messages: organiser posts and team threads.
import type { Call } from "@kit/api/call";
import { E, seg, type Untyped } from "./_e";

export const announcements = (call: Call, slug: string) => call<Untyped>("GET", E(slug, "announcements"));
export const post = (call: Call, slug: string, body: Untyped) => call<Untyped>("POST", E(slug, "announcements"), body);
export const preview = (call: Call, slug: string, body: Untyped) => call<Untyped>("POST", E(slug, "announcements", "preview"), body);
export const edit = (call: Call, slug: string, id: string, body: Untyped) => call<Untyped>("PUT", E(slug, "announcements", seg(id)), body);
export const removeAnnouncement = (call: Call, slug: string, id: string) => call<Untyped>("DELETE", E(slug, "announcements", seg(id)));
export const messageTeams = (call: Call, slug: string, body: Untyped) => call<Untyped>("POST", E(slug, "team-messages"), body);
export const teamThread = (call: Call, slug: string, team: string) => call<Untyped>("GET", E(slug, "teams", seg(team), "messages"));
export const myThread = (call: Call, slug: string) => call<Untyped>("GET", E(slug, "team", "messages"));
export const messageOrganisers = (call: Call, slug: string, body: Untyped) => call<Untyped>("POST", E(slug, "team", "messages"), body);
