import { describe, expect, it } from "vitest";
import { siteFilter, userFilter, type Person, type Site } from "./data";

const site = (o: Partial<Site>) => ({ taken_down: false, owner_suspended: false, offline: false, access: "open", domain: "", short_name: "", listed: true, ...o }) as Site;

describe("admin filters", () => {
  it("sorts sites into the chips", () => {
    expect(siteFilter("down", site({ owner_suspended: true }))).toBe(true);
    expect(siteFilter("down", site({}))).toBe(false);
    expect(siteFilter("locked", site({ access: "viewers" }))).toBe(true);
    expect(siteFilter("domain", site({ short_name: "club" }))).toBe(true);
    expect(siteFilter("unlisted", site({ listed: false }))).toBe(true);
    expect(siteFilter("all", site({ offline: true }))).toBe(true);
  });
  it("sorts people into the chips", () => {
    expect(userFilter("sites", { sites: 2 } as Person)).toBe(true);
    expect(userFilter("suspended", { suspended: false } as Person)).toBe(false);
  });
});
