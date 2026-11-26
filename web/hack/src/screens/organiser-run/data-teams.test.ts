import { describe, expect, it } from "vitest";
import { adminsLine, agoText, entryPill, fillLabel, fillSize, joinAnd, planSignature, roomFor, sitePill, teamsSubtitle, type Team } from "./data-teams";

const team = (o: Partial<Team>): Team => ({ id: "t", slug: "t", name: "T", members: [], ...o });

describe("teams words", () => {
  it("says the subtitle", () => {
    expect(teamsSubtitle(1, 0)).toBe("1 team · Everyone is on a team");
    expect(teamsSubtitle(6, 2)).toBe("6 teams · 2 people not on a team");
    expect(teamsSubtitle(0, 1)).toBe("0 teams · 1 person not on a team");
  });
  it("names admins", () => {
    expect(adminsLine(team({ members: [{ user_id: "a", display_name: "Ayu", admin: true }] }))).toBe("Admin: Ayu");
    expect(adminsLine(team({ members: [{ user_id: "a", admin: true }, { user_id: "b", display_name: "Nadia", admin: true }] }))).toBe("Admins: Someone, Nadia");
    expect(adminsLine(team({}))).toBe("");
  });
  it("joins names", () => {
    expect(joinAnd([])).toBe("");
    expect(joinAnd(["A", "B", "C"])).toBe("A, B and C");
  });
  it("places only on teams with room that were not kept", () => {
    const ts = [team({ slug: "a", members: [{ user_id: "1" }] }), team({ slug: "b", kept: true }), team({ slug: "c", locked: true }), team({ slug: "d", members: [{ user_id: "1" }, { user_id: "2" }] })];
    expect(roomFor(ts, 2).map((t) => t.slug)).toEqual(["a", "c"]);
    expect(roomFor(ts, 2, "a").map((t) => t.slug)).toEqual(["c"]);
  });
  it("compares plans regardless of order", () => {
    const a = [{ team: "x", name: "X", members: [{ user_id: "2" }, { user_id: "1" }] }, { team: "a", name: "A", members: [] }];
    const b = [{ team: "a", name: "A", members: [] }, { team: "x", name: "X", members: [{ user_id: "1" }, { user_id: "2" }] }];
    expect(planSignature(a)).toBe(planSignature(b));
    expect(planSignature(a)).not.toBe(planSignature([]));
  });
  it("labels the fill button and checks the size", () => {
    expect(fillLabel([])).toBe("Nothing to place");
    expect(fillLabel([{ team: "a", name: "A", members: [{ user_id: "1" }] }])).toBe("Place 1 person");
    expect(fillSize("3", 4)).toBe(3);
    expect(fillSize("5", 4)).toBe(0);
    expect(fillSize("2.5", 4)).toBe(0);
    expect(fillSize("", 4)).toBe(0);
  });
  it("says what an entry misses and how the site is", () => {
    expect(entryPill(null, ["title"]).text).toBe("No fields required");
    expect(entryPill({ team: { slug: "a" }, missing: [] }, ["title"]).text).toBe("Complete");
    expect(entryPill({ team: { slug: "a" }, missing: ["title", "one_of"] }, ["title"]).text).toBe("Missing: project name and a video or website");
    expect(sitePill(team({})).text).toBe("Not published");
    expect(sitePill(team({ site: { exists: true, taken_down: true } })).text).toBe("Taken down");
    expect(sitePill(team({ site: { exists: true } })).text).toBe("Live");
  });
  it("says how long ago", () => {
    const now = Date.parse("2026-11-20T10:00:00Z");
    expect(agoText("2026-11-20T09:59:30Z", now)).toBe("just now");
    expect(agoText("2026-11-20T07:00:00Z", now)).toBe("3 hours ago");
    expect(agoText("2026-11-19T10:00:00Z", now)).toBe("1 day ago");
    expect(agoText("2026-11-21T10:00:00Z", now)).toBe("");
  });
});
