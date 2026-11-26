import { describe, expect, it } from "vitest";
import { matchesRole, personBits, visibleApplications, type Application, type Person } from "./data-people";
import { linkFor, postedText, summary } from "./data-announcements";

const apps: Application[] = [
  { user_id: "a", display_name: "A", status: "pending", joined_via: "applied", age_range: "18_24", country: "ID" },
  { user_id: "b", display_name: "B", status: "pending", joined_via: "link", country: "MY" },
  { user_id: "c", display_name: "C", status: "pending", joined_via: "applied", age_range: "under_18", country: "ID" },
];

describe("people", () => {
  it("filters and sorts applications", () => {
    expect(visibleApplications(apps, { from: "link", age: "", country: "", sort: "oldest" }).map((a) => a.user_id)).toEqual(["b"]);
    expect(visibleApplications(apps, { from: "applied", age: "", country: "ID", sort: "newest" }).map((a) => a.user_id)).toEqual(["c", "a"]);
    expect(visibleApplications(apps, { from: "all", age: "under_18", country: "", sort: "oldest" }).map((a) => a.user_id)).toEqual(["c"]);
  });
  it("filters people by role", () => {
    const co: Person = { user_id: "x", role: "organiser" };
    const main: Person = { user_id: "y", role: "organiser", primary_organiser: true };
    expect(matchesRole(co, "Co-organisers")).toBe(true);
    expect(matchesRole(main, "Co-organisers")).toBe(false);
    expect(matchesRole(main, "Organisers")).toBe(true);
  });
  it("says Applied for someone not let in", () => {
    expect(personBits({ user_id: "p", role: "participant", approval_status: "pending" }, "Wed 18 Nov")).toBe("Participant · Applied Wed 18 Nov");
    expect(personBits({ user_id: "p", role: "participant", approval_status: "rejected" }, "")).toBe("Participant · Not approved");
    expect(personBits({ user_id: "p", role: "participant", team: { name: "Lumbung" } }, "Wed 18 Nov")).toBe("Participant · Lumbung · Joined Wed 18 Nov");
  });
});

describe("announcements", () => {
  it("plain summary", () => expect(summary("See **the** [page](https://x.org/a)")).toBe("See the page"));
  it("posted line", () => {
    expect(postedText(false, {})).toEqual({ text: "Posted" });
    expect(postedText(true, { emails_queued: 0 }).bad).toBe(true);
    expect(postedText(true, { emails_queued: 1, organiser_copied: true }).text).toBe("Posted and emailed to 1 person (including a copy to you).");
  });
  it("link checks", () => {
    expect(linkFor("example.org/x", "Docs")).toEqual({ text: "[Docs](https://example.org/x)" });
    expect(linkFor("javascript:alert(1)", "")).toEqual({ error: "Use an http or https address." });
    expect(linkFor("https://a.org/(x)", "")).toEqual({ text: "https://a.org/%28x%29" });
    expect(linkFor("https://a.org", "a]b")).toEqual({ error: "Use a label without brackets or line breaks." });
  });
});
