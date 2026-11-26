// Judging: rubric, assignment, conflicts, scoring.
import type { Call } from "@kit/api/call";
import { E, seg, type Untyped } from "./_e";

export const rubric = (call: Call, slug: string) => call<Untyped>("GET", E(slug, "rubric"));
export const setRubric = (call: Call, slug: string, body: Untyped) => call<Untyped>("PUT", E(slug, "rubric"), body);
export const settings = (call: Call, slug: string) => call<Untyped>("GET", E(slug, "judging", "settings"));
export const setSettings = (call: Call, slug: string, body: Untyped) => call<Untyped>("PATCH", E(slug, "judging", "settings"), body);
export const dashboard = (call: Call, slug: string) => call<Untyped>("GET", E(slug, "judging", "dashboard"));
export const preview = (call: Call, slug: string) => call<Untyped>("GET", E(slug, "judging", "preview"));
export const panels = (call: Call, slug: string) => call<Untyped>("GET", E(slug, "judging", "panels"));
export const setPanels = (call: Call, slug: string, body: Untyped) => call<Untyped>("PUT", E(slug, "judging", "panels"), body);
export const assignments = (call: Call, slug: string) => call<Untyped>("GET", E(slug, "assignments"));
export const setAssignments = (call: Call, slug: string, body: Untyped) => call<Untyped>("PUT", E(slug, "assignments"), body);
export const generateAssignments = (call: Call, slug: string, body: Untyped = {}) => call<Untyped>("POST", E(slug, "assignments", "generate"), body);
export const conflicts = (call: Call, slug: string, mine = false) => call<Untyped>("GET", E(slug, "conflicts") + (mine ? "?mine=1" : ""));
export const declareConflict = (call: Call, slug: string, body: Untyped) => call<Untyped>("POST", E(slug, "conflicts"), body);
export const removeConflict = (call: Call, slug: string, team: string) => call<Untyped>("DELETE", E(slug, "conflicts", seg(team)));
export const removeFormerJudge = (call: Call, slug: string, user: string) => call<Untyped>("DELETE", E(slug, "judging", "former-judges", seg(user)));
export const queue = (call: Call, slug: string) => call<Untyped>("GET", E(slug, "judge", "queue"));
export const scores = (call: Call, slug: string, team: string) => call<Untyped>("GET", E(slug, "judge", "scores", seg(team)));
export const score = (call: Call, slug: string, team: string, body: Untyped) => call<Untyped>("PUT", E(slug, "judge", "scores", seg(team)), body);
/** The judge's own acknowledgements (rubric read, no known teams): the rubric's carries the version shown. */
export const acknowledge = (call: Call, slug: string, id: string, version?: string) =>
  call<Untyped>("POST", E(slug, "checklist", "acknowledgements", seg(id)), version ? { version } : {});
