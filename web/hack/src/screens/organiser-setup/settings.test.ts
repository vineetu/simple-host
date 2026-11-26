import { describe, expect, it } from "vitest";
import { addReminder, fieldsEqual, impactLines, offsetLabel, PRESETS, presetLabel, receiverName, testLine, withField, withIds } from "./settings";

describe("settings logic", () => {
  it("names reminder offsets", () => {
    expect(offsetLabel(1440)).toBe("1 day");
    expect(offsetLabel(4320)).toBe("3 days");
    expect(offsetLabel(120)).toBe("2 hours");
    expect(offsetLabel(90)).toBe("90 minutes");
  });
  it("checks a new reminder", () => {
    const lim = { min_minutes: 30, max_minutes: 20160, max_count: 3 };
    expect(addReminder([], "x", "days", lim)).toBe("Enter a whole number.");
    expect(addReminder([], "10", "minutes", lim)).toMatch(/between 30 minutes/);
    expect(addReminder([60], "1", "hours", lim)).toBe("Already have a reminder at that time.");
    expect(addReminder([60, 120, 180], "1", "days", lim)).toBe("Up to 3 reminders.");
    expect(addReminder([60], "1", "days", lim)).toEqual([1440, 60]);
  });
  it("keeps a preset until a line differs", () => {
    const r = { preset: "video_or_website", fields: { ...PRESETS.video_or_website.fields } };
    expect(withField(r, "audience", "optional").preset).toBe("video_or_website");
    expect(withField(r, "audience", "required").preset).toBe("custom");
    expect(fieldsEqual({ a: "optional" }, {})).toBe(true);
    expect(presetLabel("custom", "video_or_website")).toBe("Custom (from Video or website)");
  });
  it("says what a rule change does", () => {
    expect(impactLines({})).toEqual([]);
    expect(impactLines({ submitted_missing: 1, drafts_newly_blocked: 2 })).toEqual([
      "1 submitted entry does not meet what you are about to ask for. They stay submitted, and their teams see that you now ask for more.",
      "2 drafts will need more before they can submit.",
    ]);
  });
  it("gives new questions unused ids", () => {
    expect(withIds([{ id: "q2", prompt: "a", required: false }, { id: "", prompt: "b", required: true }, { id: "", prompt: "c", required: false }]).map((q) => q.id)).toEqual(["q2", "q1", "q3"]);
  });
  it("names webhooks and test results", () => {
    expect(receiverName({ id: "1", url: "https://hooks.example.com/x" })).toBe("hooks.example.com");
    expect(receiverName({ id: "1", source: "mcp" })).toBe("Connected app");
    expect(testLine({ delivered: false, result: "timeout", status: 0 })).toBe("Receiver took too long.");
    expect(testLine({ delivered: true, status: 200 })).toBe("Test delivered (HTTP 200).");
  });
});
