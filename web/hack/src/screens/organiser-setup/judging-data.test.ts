import { describe, expect, it } from "vitest";
import { formerMeta, gridOf, noTrackNote, payload, sameTicks, teamProgress, ticked } from "./judging-data";

describe("judging data", () => {
  it("warns when fewer judges can score a team than the mode asks for", () => {
    const rows = teamProgress([{ team_id: "a", team_name: "A", judges_scored: 0, judges_eligible: 1 }, { team_id: "b", team_name: "B", judges_scored: 1, judges_eligible: 2 }], { assignment_mode: "manual", judges_per_team: 2 });
    expect(rows.map((r) => [r.text, r.short])).toEqual([["No scores yet", "Needs 2 judges, 1 can score it"], ["1 judge scored", ""]]);
    expect(teamProgress([{ judges_scored: 0, judges_eligible: 0 }], { assignment_mode: "open", judges_per_team: 2 })[0].short).toBe("");
  });
  it("names the teams Panel mode cannot judge", () => {
    expect(noTrackNote([{ id: "1", name: "Kopi", track: null }, { id: "2", track: { id: "t" } }])).toBe("1 team with no track won’t be judged until it picks a track, or you assign one: Kopi.");
    expect(noTrackNote([{ id: "2", track: { id: "t" } }])).toBe("");
  });
  it("drops an impossible “of” for a former judge", () => {
    expect(formerMeta({ user_id: "u", counted_teams: 4, assigned_count: 6 })).toBe("Scored 4 of 6, still counted");
    expect(formerMeta({ user_id: "u", counted_teams: 4, assigned_count: 0 })).toBe("4 teams scored, still counted");
  });
  it("shows the saved mode's grid unless ?grid= names one", () => {
    expect(gridOf(null, "panel")).toBe("panel");
    expect(gridOf(null, "automatic")).toBe("manual");
    expect(gridOf("panel", "manual")).toBe("panel");
  });
  it("leaves conflicted pairs out of Manual's ticks and sends the rest", () => {
    const t = ticked("manual", [{ judge_id: "j1", team_id: "a" }, { judge_id: "j2", team_id: "a" }], [{ judge_id: "j2", team_id: "a" }]);
    expect([...t]).toEqual(["j1|a"]);
    expect(payload("manual", t)).toEqual({ assignments: [{ judge_id: "j1", team_id: "a" }] });
    expect(payload("panel", new Set(["j1|t1"]))).toEqual({ panels: [{ judge_id: "j1", track_id: "t1" }] });
    expect(sameTicks(new Set(["a"]), new Set(["a"]))).toBe(true);
  });
});
