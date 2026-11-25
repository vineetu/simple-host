import { describe, expect, it } from "vitest";
import { ago, bytes, localPath, searchMatches, short } from "./format";

describe("format", () => {
  it("writes sizes and counts for people", () => {
    expect(bytes(0)).toBe("0 B");
    expect(bytes(2048)).toBe("2 KB");
    expect(bytes(5 * 1048576)).toBe("5.0 MB");
    expect(bytes(1099511627776)).toBe("1 TB");
    expect(short(950)).toBe("950");
    expect(short(1200)).toBe("1.2k");
    expect(short(2_500_000)).toBe("2.5M");
  });
  it("says how long ago in calendar days", () => {
    const now = new Date(2026, 10, 25, 9);
    expect(ago("2026-11-25T01:00:00", now)).toBe("today");
    expect(ago("2026-11-24T23:00:00", now)).toBe("yesterday");
    expect(ago("2026-09-01T12:00:00", now)).toBe("2 months ago");
    expect(ago(null, now)).toBe("");
  });
  it("searches with every word and hides -words", () => {
    expect(searchMatches("ana rec", "recipes ana")).toBe(true);
    expect(searchMatches("ana -rec", "recipes ana")).toBe(false);
    expect(searchMatches("", "anything")).toBe(true);
  });
  it("keeps next on this host", () => {
    expect(localPath("/dashboard")).toBe(true);
    expect(localPath("//evil.example")).toBe(false);
    expect(localPath("https://evil.example")).toBe(false);
  });
});
