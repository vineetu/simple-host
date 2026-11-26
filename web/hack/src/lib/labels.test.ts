import { describe, expect, it } from "vitest";
import { initials, joinNames, markInk, possessive, safeHref, stageLabel } from "./labels";

describe("labels", () => {
  it("takes initials from words with letters", () => {
    expect(initials("Mock · Voting")).toBe("MV");
    expect(initials("hack night 2026")).toBe("HN");
  });
  it("joins names", () => {
    expect(joinNames([])).toBe("They");
    expect(joinNames(["Ayu", "Nadia", "2 others"])).toBe("Ayu, Nadia and 2 others");
  });
  it("picks ink for a colour", () => {
    expect(markInk("#ffee00")).toBe("#2b2925");
    expect(markInk("#a33f31")).toBe("#fff");
  });
  it("refuses links that are not http(s)", () => {
    expect(safeHref("javascript:alert(1)")).toBe("");
    expect(safeHref(" https://a.b ")).toBe("https://a.b");
  });
  it("says stages and possessives plainly", () => {
    expect(stageLabel("archived")).toBe("Ended");
    expect(possessive("Chris")).toBe("Chris’");
  });
});

import { markColours } from "./labels";
describe("markColours", () => {
  it("keeps a colour either ink reads on, and darkens one neither does", () => {
    expect(markColours("#216c6a")).toEqual({ bg: "#216c6a", ink: "#fff" });
    const m = markColours("#2a78d6");
    expect(m.ink).toBe("#fff");
    expect(m.bg).not.toBe("#2a78d6");
  });
});
