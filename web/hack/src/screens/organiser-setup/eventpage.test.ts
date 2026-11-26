import { describe, expect, it } from "vitest";
import { ApiError } from "@kit/api/call";
import { activeVersion, anotherTheme, archiveErrorText, checkImage, checkTime, groupByDay, label, merge, move, moveTo, setInLink, slugify, sponsorLine, wantsAnnouncements, when, type FixedRow } from "./eventpage-data";

const Z = "Asia/Jakarta";

describe("event page logic", () => {
  it("sends old announcements links to their own screen", () => {
    expect(wantsAnnouncements("?s=event-page&tab=announcements", "")).toBe(true);
    expect(wantsAnnouncements("?s=event-page", "#announcements")).toBe(true);
    expect(wantsAnnouncements("?s=event-page&tab=faq", "#announcements")).toBe(false);
    expect(wantsAnnouncements("?s=event-page", "#%E0")).toBe(false);
  });

  it("merges typed and fixed rows in time order and marks now and next", () => {
    const typed = [{ title: "Demos", start_at: "2026-11-28T08:00:00Z", end_at: "2026-11-28T09:00:00Z" }];
    const fixed: FixedRow[] = [{ key: "event_start", title: "Event starts", start_at: "2026-11-28T01:00:00Z", fixed: true, set_in: "basics" }];
    const rows = label(merge(typed, fixed, Z), Date.parse("2026-11-28T08:30:00Z"));
    expect(rows.map((r) => r.title)).toEqual(["Event starts", "Demos"]);
    expect(rows[1].now).toBe(true);
    expect(rows[1].src).toBe(typed[0]);
    expect(groupByDay(rows, Z)).toHaveLength(1);
    expect(when(rows[1], Z)).toEqual(["3:00 pm", "– 4:00 pm"]);
  });

  it("sorts a date-only end after the day's other rows", () => {
    const fixed: FixedRow[] = [{ key: "event_end", title: "Event ends", start_at: "2026-11-28T17:00:00Z", all_day: true, fixed: true }];
    const rows = merge([{ title: "Late", start_at: "2026-11-29T15:00:00Z", end_at: "2026-11-29T16:00:00Z" }], fixed, Z);
    expect(rows[0].title).toBe("Late");
  });

  it("checks a schedule row", () => {
    expect(checkTime("", "2026-11-28", "09:00", "2026-11-28", "10:00")).toEqual({ title: "Title is required." });
    expect(checkTime("A", "2026-11-28", "09:00", "", "")).toEqual({ end: "Starts and ends are both required." });
    expect(checkTime("A", "2026-11-28", "09:00", "2026-11-28", "08:00")).toEqual({ end: "Ends must be after it starts." });
    expect(checkTime("A", "2026-11-28", "09:00", "2026-11-28", "10:00")).toEqual({});
  });

  it("moves rows", () => {
    expect(move([1, 2, 3], 0, 1)).toEqual([2, 1, 3]);
    expect(move([1, 2, 3], 0, -1)).toEqual([1, 2, 3]);
    expect(moveTo([1, 2, 3], 0, 2)).toEqual([2, 3, 1]);
  });

  it("words sponsors, images, tracks, themes and links", () => {
    expect(sponsorLine({ name: "A", tier: "Gold", url: "https://acme.example/x" })).toBe("Gold · acme.example");
    expect(sponsorLine({ name: "A" })).toBe("No link");
    expect(checkImage({ type: "image/gif", size: 1 })).toMatch(/PNG/);
    expect(checkImage({ type: "image/png", size: 200 * 1024 })).toMatch(/128 KB/);
    expect(checkImage({ type: "image/png", size: 1024 })).toBe("");
    expect(slugify("Food & Waste!")).toBe("food-waste");
    expect(anotherTheme("poster", () => 0)).toBe("confetti");
    expect(setInLink("entries", "x", "entry_deadline")?.href).toBe("/e/x/manage?s=settings&tab=entries#deadline");
    expect(setInLink("results", "x", "voting")?.label).toBe("Settings → Results");
  });

  it("words zip refusals and finds the live version", () => {
    expect(archiveErrorText(new ApiError(400, "missing_index", "x"))).toMatch(/index.html/);
    expect(archiveErrorText(new ApiError(400, "invalid_archive", "unsafe path ../a", { error: "unsafe path ../a" }))).toMatch(/file paths/);
    expect(archiveErrorText(new ApiError(400, "invalid_archive", "bad"))).toBe("That file isn't a valid .zip.");
    const w = { versions: [{ version_number: 1, created_at: "" }, { version_number: 2, created_at: "" }], active_version: 1 };
    expect(activeVersion(w)?.version_number).toBe(1);
    expect(activeVersion({ versions: w.versions })?.version_number).toBe(2);
  });
});
