import { describe, expect, it } from "vitest";
import { dateRange, ineligible, intro } from "./directory";

describe("directory dates", () => {
  it("shortens a range of whole days", () => {
    expect(dateRange("2026-12-03T00:00:00+07:00", "2026-12-05T00:00:00+07:00", "Asia/Jakarta")).toBe("3–5 Dec 2026");
    expect(dateRange("2026-11-30T00:00:00Z", "2026-12-02T00:00:00Z", "UTC")).toBe("30 Nov–2 Dec 2026");
    expect(dateRange("2026-12-31T00:00:00Z", "2027-01-02T00:00:00Z", "UTC")).toBe("31 Dec 2026–2 Jan 2027");
    expect(dateRange("2026-12-03T00:00:00Z", "2026-12-03T00:00:00Z", "UTC")).toBe("3 Dec 2026");
  });
  it("shows a time that is not midnight", () => {
    expect(dateRange("2026-12-03T18:30:00Z", null, "UTC")).toBe("3 Dec 2026, 6:30 pm");
    expect(dateRange(null, null)).toBe("");
  });
});

describe("vote words", () => {
  it("says who may vote and what is happening", () => {
    expect(ineligible("participants")).toBe("Only approved participants can vote in this event.");
    expect(ineligible("x")).toBe("Your account is not eligible to vote in this event.");
    expect(intro("upcoming")).toBe("Voting opens soon.");
  });
});
