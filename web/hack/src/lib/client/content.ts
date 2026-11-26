// Content: public page sections, website mode, icon, tracks.
import type { Call } from "@kit/api/call";
import { E, type Untyped } from "./_e";

export const get = (call: Call, slug: string) => call<Untyped>("GET", E(slug, "content"));
export const set = (call: Call, slug: string, body: Untyped) => call<Untyped>("PUT", E(slug, "content"), body);
export const website = (call: Call, slug: string) => call<Untyped>("GET", E(slug, "website"));
export const setWebsite = (call: Call, slug: string, body: Untyped) => call<Untyped>("PATCH", E(slug, "website"), body);
export const icon = (call: Call, slug: string) => call<Untyped>("GET", E(slug, "icon"));
/** The icon's bytes go up with bytes upload (PUT E/icon), not as JSON. */
export const iconPath = (slug: string) => E(slug, "icon");
export const removeIcon = (call: Call, slug: string) => call<Untyped>("DELETE", E(slug, "icon"));
export const tracks = (call: Call, slug: string) => call<Untyped>("GET", E(slug, "tracks"));
export const setTracks = (call: Call, slug: string, body: Untyped) => call<Untyped>("PUT", E(slug, "tracks"), body);
export const setTeamTrack = (call: Call, slug: string, body: Untyped) => call<Untyped>("PUT", E(slug, "team", "track"), body);
