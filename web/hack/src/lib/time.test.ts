import { describe, expect, it } from "vitest";
import { dateTime, isLocalMidnight, nextDeadline } from "./time";

describe("time", () => {
  it("formats in the event's zone, 12-hour, weekday first", () => {
    expect(dateTime("2030-10-28T18:27:00Z", "UTC")).toBe("Mon 28 Oct 2030, 6:27 pm");
    expect(dateTime("2030-10-28", "Asia/Jakarta")).toBe("Mon 28 Oct 2030");
  });
  it("knows a local midnight", () => {
    expect(isLocalMidnight("2030-10-27T17:00:00Z", "Asia/Jakarta")).toBe(true);
  });
  it("shows the next date that matters at the stage", () => {
    const ev = { stage: "building", time_zone: "UTC", starts_at: "2030-01-01T00:00:00Z", submission_deadline: "2030-01-03T12:00:00Z", ends_at: "2030-01-04T00:00:00Z" };
    expect(nextDeadline(ev, Date.parse("2030-01-02T00:00:00Z"))).toEqual(["Entries close", "2030-01-03T12:00:00Z", false]);
    expect(nextDeadline(ev, Date.parse("2030-01-03T13:00:00Z"))).toEqual(["Ends", "2030-01-04T00:00:00Z", true]);
    expect(nextDeadline({ stage: "results" })).toBeNull();
  });
});
