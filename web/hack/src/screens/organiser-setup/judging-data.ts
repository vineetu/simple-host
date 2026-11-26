// Judging and the assignment grid: the plain logic (tested in judging-data.test.ts).
import type { Conflict, Dashboard, JudgingSettings } from "@/lib/client/judging";

export type TeamRow = { id: string; slug?: string; name?: string; track?: { id?: string; name?: string } | null };
type DashTeam = NonNullable<Dashboard["teams"]>[number];
type Former = NonNullable<Dashboard["former_judges"]>[number];

/** teamProgress is each team's scored line, and the warning when fewer judges can score it than the mode asks for. */
export function teamProgress(rows: DashTeam[], s: JudgingSettings) {
  const mode = s.assignment_mode || "open";
  const target = mode !== "open" ? s.judges_per_team || 0 : 0;
  return rows.map((t, i) => ({
    id: t.team_id || String(i),
    name: t.team_name || "A team",
    text: t.judges_scored === 0 ? "No scores yet" : `${t.judges_scored} judge${t.judges_scored === 1 ? "" : "s"} scored`,
    short: target > 0 && t.judges_eligible < target ? `Needs ${target} judges, ${t.judges_eligible} can score it` : "",
  }));
}

/** noTrackNote names the teams Panel mode cannot judge because they have no track ('' when none). */
export function noTrackNote(teams: TeamRow[]): string {
  const none = teams.filter((t) => !t.track);
  if (!none.length) return "";
  const n = none.length;
  return `${n} team${n === 1 ? "" : "s"} with no track won’t be judged until ${n === 1 ? "it picks" : "they pick"} a track, or you assign one: ${none.map((t) => t.name || "A team").join(", ")}.`;
}

/** formerMeta is a former judge's line; an "of" smaller than what they scored is left out. */
export const formerMeta = (f: Former) =>
  f.assigned_count >= f.counted_teams && f.assigned_count > 0
    ? `Scored ${f.counted_teams} of ${f.assigned_count}, still counted`
    : `${f.counted_teams} team${f.counted_teams === 1 ? "" : "s"} scored, still counted`;

export type Grid = "manual" | "panel";
export type Pair = { judge_id: string; subject: string };

/** gridOf is the grid shown: ?grid= when it names one, else the saved mode's (Panel, or Manual). */
export const gridOf = (wanted: string | null, mode?: string): Grid => (wanted === "manual" || wanted === "panel" ? wanted : mode === "panel" ? "panel" : "manual");

/** key is one tick of the grid. */
export const key = (judge: string, subject: string) => judge + "|" + subject;

/** conflicted is whether a judge knows someone on a team (only Manual's grid dims these). */
export const conflicted = (conflicts: Conflict[], judge: string, team: string) => conflicts.some((c) => c.team_id === team && c.judge_id === judge);

/** ticked is the saved ticks of the grid, leaving out conflicted pairs in Manual. */
export function ticked(grid: Grid, rows: { judge_id: string; team_id?: string; track_id?: string }[], conflicts: Conflict[]): Set<string> {
  const out = new Set<string>();
  for (const r of rows) {
    const subject = grid === "manual" ? r.team_id : r.track_id;
    if (!subject) continue;
    if (grid === "manual" && conflicted(conflicts, r.judge_id, subject)) continue;
    out.add(key(r.judge_id, subject));
  }
  return out;
}

/** payload is the PUT body for the grid's ticks. */
export function payload(grid: Grid, ticks: Set<string>) {
  const rows = [...ticks].sort().map((k) => {
    const [judge_id, subject] = k.split("|");
    return grid === "manual" ? { judge_id, team_id: subject } : { judge_id, track_id: subject };
  });
  return grid === "manual" ? { assignments: rows } : { panels: rows };
}

/** sameTicks is whether two sets of ticks are the same. */
export const sameTicks = (a: Set<string>, b: Set<string>) => a.size === b.size && [...a].every((k) => b.has(k));
