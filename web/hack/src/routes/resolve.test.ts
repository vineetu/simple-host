import { describe, expect, it } from "vitest";
import cases from "./resolve-cases.json";
import { needHref, resolveScreen } from "./resolve";

describe("resolveScreen", () => {
  for (const c of cases)
    it(c.url, () => {
      const u = new URL(c.url, "https://simple-hack.app");
      expect(resolveScreen(u.pathname, u.search).id).toBe(c.id);
    });
  it("names the event and the area", () => {
    expect(resolveScreen("/e/a%20b/team", "?s=entry")).toEqual({ id: "participant-entry", section: "entry", slug: "a b", role: "participant" });
  });
  it("links a to-do into the person's own area", () => {
    expect(needHref("organiser", "x", { section: "team", team: "red" })).toBe("/e/x/manage?s=team&team=red");
    expect(needHref("participant", "x", { section: "home" })).toBe("/e/x/team");
  });
});
