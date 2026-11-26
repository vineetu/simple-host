import { describe, expect, it } from "vitest";
import { addTool, conflicts, diff, fieldOf, marks, mode, need, valuesOf, type EntryData } from "./data";

describe("Entry rules", () => {
  it("reads a missing rule as Optional, the name as always Required, and Track as Off with no tracks", () => {
    expect(mode({ fields: {} }, true, "problem")).toBe("optional");
    expect(mode({ fields: { title: "off" } }, true, "title")).toBe("required");
    expect(mode({ fields: { track: "required" } }, false, "track")).toBe("off");
  });
  it("names what is missing as the form labels it", () => {
    const data: EntryData = { missing: ["tools", "one_of", "code_url"], rules: { summary: "a video or a published website" }, progress: { steps: [{ id: "summary", filled: 5, of: 6 }] } };
    expect(need(data)).toEqual([
      { text: "Summary card · 5 of 6 · Missing: Tools and AI", ok: false },
      { text: "A video or a published website · Missing: Code link", ok: false },
    ]);
    expect(need({ missing: [], rules: {} })).toEqual([{ text: "Summary card", ok: true }]);
  });
  it("puts a refused submit's lines under each field, the group's on show-your-work", () => {
    expect(marks(["tools", "one_of"], { summary: "a video" })).toEqual({ tools: "Add at least one tool.", one_of: "Add a video." });
    expect(fieldOf("invalid_url", { video_url: "x" })).toEqual(["video_url"]);
    expect(fieldOf("invalid_title", {})).toEqual(["title"]);
  });
});

describe("Entry edits", () => {
  const e = { title: "A", video_url: "", tools: ["Claude"], track: { slug: "food" }, maturity: "partial" as const };
  it("sends only what changed, links trimmed", () => {
    const v = { ...valuesOf(e), title: "B", video_url: " https://youtu.be/x ", tools: ["Claude", "Figma"] };
    expect(diff(e, v, { track: true })).toEqual({ title: "B", video_url: "https://youtu.be/x", tools: ["Claude", "Figma"] });
    expect(diff(e, valuesOf(e), { track: true })).toEqual({});
  });
  it("adds a tool once, trimmed to 30 characters, up to 10", () => {
    expect(addTool(["Claude"], " claude ")).toEqual(["Claude"]);
    expect(addTool([], "x".repeat(40))[0]).toHaveLength(30);
    expect(addTool(Array.from({ length: 10 }, (_, i) => "t" + i), "more")).toHaveLength(10);
  });
  it("lists only the lines a teammate's save differs on", () => {
    expect(conflicts({ title: "Mine" }, { title: "Old", problem: "P" }, { title: "Theirs", problem: "P" })).toEqual([{ label: "title", mine: "Mine", theirs: "Theirs" }]);
  });
});
