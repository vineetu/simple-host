// Voting and results: people's choice, directory listing, publishing, standings.
import type { Call } from "@kit/api/call";
import { E, type Untyped } from "./_e";

export const settings = (call: Call, slug: string) => call<Untyped>("GET", E(slug, "voting"));
export const setSettings = (call: Call, slug: string, body: Untyped) => call<Untyped>("PUT", E(slug, "voting"), body);
export const setDirectory = (call: Call, slug: string, body: Untyped) => call<Untyped>("PATCH", E(slug, "directory"), body);
export const ballot = (call: Call, slug: string) => call<Untyped>("GET", E(slug, "vote"));
export const vote = (call: Call, slug: string, body: Untyped) => call<Untyped>("PUT", E(slug, "vote"), body);
export const myVote = (call: Call, slug: string) => call<Untyped>("GET", E(slug, "my-vote"));
export const publish = (call: Call, slug: string, body: Untyped = {}) => call<Untyped>("POST", E(slug, "results", "publish"), body);
export const results = (call: Call, slug: string) => call<Untyped>("GET", E(slug, "results"));
export const setResults = (call: Call, slug: string, body: Untyped) => call<Untyped>("PATCH", E(slug, "results"), body);
export const myResults = (call: Call, slug: string) => call<Untyped>("GET", E(slug, "my-results"));
