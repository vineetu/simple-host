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

// Organiser setup (Judging, Assignment grid): the shapes they read, and the two
// calls that carry more than the plain routes above.
export type JudgeRow = { user_id: string; name?: string; email?: string };
export type JudgingSettings = { assignment_mode?: "open" | "automatic" | "manual" | "panel"; judges_per_team?: number; judges?: JudgeRow[] } & Untyped;
export type Conflict = { team_id: string; judge_id: string; team_name?: string; judge_name?: string };
export type Dashboard = {
  teams?: { team_id?: string; team_name?: string; judges_scored: number; judges_eligible: number }[];
  judges?: { judge_id?: string; judge_name?: string; done_count: number; assigned_count: number }[];
  former_judges?: { user_id: string; display_name?: string; counted_teams: number; assigned_count: number }[];
};
/** An organiser removes one judge's conflict with a team. */
export const removeJudgeConflict = (call: Call, slug: string, team: string, judge: string) =>
  call<Untyped>("DELETE", E(slug, "conflicts", seg(team)) + "?judge_user_id=" + seg(judge));
/** Deleting a former judge's scores repeats the event's address name, as the server requires. */
export const deleteFormerJudgeScores = (call: Call, slug: string, user: string) => call<Untyped>("DELETE", E(slug, "judging", "former-judges", seg(user)), { confirm: slug });
