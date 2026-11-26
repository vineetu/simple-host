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

/** GET E/vote: the people's choice ballot, public. */
export type Ballot = {
  title: string;
  status: "open" | "upcoming" | "closed" | string;
  time_zone?: string;
  opens_at?: string | null;
  closes_at?: string | null;
  eligibility?: string;
  options?: { team: string; team_name: string; title: string; tagline?: string; url: string }[];
  ranking?: { team: string; team_name: string; votes: number }[];
};
/** GET E/my-vote: the signed-in person's vote and whether they may vote. */
export type MyVote = { team?: string; eligible?: boolean; own_team?: string };
export const ballotOf = (call: Call, slug: string) => call<Ballot>("GET", E(slug, "vote"));
export const myVoteOf = (call: Call, slug: string) => call<MyVote>("GET", E(slug, "my-vote"));
export const castVote = (call: Call, slug: string, team: string) => call<{ changed?: boolean }>("PUT", E(slug, "vote"), { team });
