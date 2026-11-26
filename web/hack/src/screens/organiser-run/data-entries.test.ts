import { describe, expect, it } from "vitest";
import { fmtBytes, joinAnd, labelList, progress, rowAction, siteWords, status, usageTotal, type EntryItem } from "./data-entries";

const e = (o: Partial<EntryItem> = {}): EntryItem => ({ team: { slug: "t", name: "T" }, ...o });

describe("entries words", () => {
  it("names the status, excluded first", () => {
    expect(status(e({ excluded: true, status: "submitted" })).label).toBe("Excluded");
    expect(status(e({ status: "submitted" })).label).toBe("Submitted");
    expect(status(e({ incomplete: true, status: "not_submitted", title: "x" })).label).toBe("Incomplete");
    expect(status(e()).label).toBe("Not started");
    expect(status(e({ title: "x" })).label).toBe("Draft");
    expect(status(e({ has_screenshot: true })).label).toBe("Draft");
  });
  it("says what is missing or when it went in", () => {
    expect(progress(e({ status: "submitted", submitted_at: "2026-01-01T00:00:00Z" }), () => "Thu 1 Jan")).toBe("Submitted Thu 1 Jan");
    expect(progress(e({ missing: ["video_url", "one_of", "zz"] }), () => "")).toBe("Missing: a video link, a video or website, zz");
    expect(progress(e({ title: "x" }), () => "")).toBe("Nothing missing");
    expect(progress(e(), () => "")).toBe("No entry yet");
  });
  it("offers include for excluded, exclude for incomplete, nothing otherwise", () => {
    expect(rowAction(e({ excluded: true, incomplete: true }))).toBe("include");
    expect(rowAction(e({ incomplete: true }))).toBe("exclude");
    expect(rowAction(e({ status: "submitted" }))).toBeNull();
  });
  it("words the site", () => {
    expect(siteWords(e())).toBe("No site yet");
    expect(siteWords(e({ site_exists: true }))).toBe("Site live");
    expect(siteWords(e({ site_exists: true, site_taken_down: true }))).toBe("Site taken down");
  });
  it("joins and sizes", () => {
    expect(joinAnd([])).toBe("");
    expect(joinAnd(labelList(["title"]))).toBe("project name");
    expect(joinAnd(["a", "b", "c"])).toBe("a, b and c");
    expect(fmtBytes(512)).toBe("512 B");
    expect(fmtBytes(1782579)).toBe("1.7 MB");
    expect(fmtBytes(20 * 1048576)).toBe("20 MB");
    expect(usageTotal({ saved_data_bytes: 1, history_bytes: 2, screenshot_bytes: 3, file_bytes: 4 })).toBe(10);
  });
});
