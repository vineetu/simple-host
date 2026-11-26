import { describe, expect, it } from "vitest";
import { countdownText, entrySection, headline, hero, label, merge, nudgeLine, scoreText, type HomeDetail, type Progress } from "./data";
import { splitNeed } from "@/shared/NeedsYou";

const ev = (o: Partial<HomeDetail["event"]> = {}) => ({ slug: "x", title: "X", stage: "building", time_zone: "UTC", ...o }) as HomeDetail["event"];
const d = (o: Omit<Partial<HomeDetail>, "event"> & { event?: Partial<HomeDetail["event"]> } = {}): HomeDetail =>
  ({ role: "participant", me: { approval_status: "approved", team: { slug: "t", name: "Lumbung" } }, ...o, event: ev(o.event) }) as HomeDetail;
const progress = (o: Partial<Progress> = {}): Progress => ({
  steps: [{ id: "team", done: true }, { id: "summary", done: false, filled: 3, of: 6, missing: ["tools", "maturity"] }, { id: "evidence", done: false }, { id: "submit", status: "draft" }],
  next: "summary",
  deadline: "2030-01-01T00:00:00Z",
  server_now: "2029-12-30T00:00:00Z",
  ...o,
});

describe("Your next step", () => {
  it("results outrank everything, ended says so", () => {
    expect(hero(d({ event: { results_published: true, stage: "archived" } }), null).title).toBe("This event has ended. Results are out.");
    expect(hero(d({ event: { stage: "archived", url: "https://x" } }), null)).toMatchObject({ title: "This event has ended", external: true });
  });
  it("no team: an invite first, then open teams, then the facts", () => {
    const none = d({ me: { approval_status: "approved", team: null } });
    expect(hero(none, { enabled: true, pending: [{ kind: "invite", team_name: "Kopi" }] })).toMatchObject({ kind: "invite", title: "You have an invite from Kopi" });
    expect(hero(none, { enabled: true, teams: [1, 2] }).note).toBe("2 teams are looking for members.");
    expect(hero(d({ me: { team: null }, event: { stage: "closed" } }), null).title).toBe("You're not on a team");
  });
  it("names up to two missing lines, then counts", () => {
    expect(hero(d({ progress: progress() }), null).title).toBe("Finish your summary card: add tools and AI and what works today");
    const three = progress({ steps: [{ id: "team", done: true }, { id: "summary", missing: ["a", "b", "c"] }] });
    expect(hero(d({ progress: three }), null).title).toBe("Finish your summary card: 3 lines left");
  });
  it("a team admin hears about join requests", () => {
    expect(hero(d({ me: { team: { slug: "t", name: "Lumbung", admin: true, pending_requests: 2 } } }), null)).toMatchObject({ title: "2 people asked to join Lumbung", label: "See requests" });
  });
});

describe("Your entry", () => {
  it("offers Submit only when ready and open", () => {
    const s = entrySection(d({ progress: progress({ next: "submit", steps: [{ id: "team", done: true }, { id: "summary", done: true, of: 6 }, { id: "evidence", done: true }, { id: "submit", status: "draft" }] }) }))!;
    expect(s.rows[3].action).toBe("submit");
    expect(s.rows[1].detail).toBe("All 6 lines filled.");
    expect(s.countdown?.left).toBe("left");
  });
  it("shows the whole path before a team, and nothing once judging starts", () => {
    const s = entrySection(d({ me: { team: null }, event: { stage: "open" } }))!;
    expect(s.rows.map((r) => r.current)).toEqual([true, false, false, false]);
    expect(entrySection(d({ event: { stage: "judging" }, progress: progress() }))).toBeNull();
  });
  it("words an extension", () => {
    const s = entrySection(d({ progress: progress({ deadline_is_extension: true, event_deadline: "2029-12-31T21:00:00Z" }) }))!;
    expect(s.deadline).toMatch(/^Your team closes .*, 3 hours after everyone else$/);
  });
});

describe("schedule", () => {
  const now = Date.parse("2026-11-20T10:00:00Z");
  const rows = merge({ schedule: [{ title: "Demos", start_at: "2026-11-20T10:10:00Z" }, { title: "Build", start_at: "2026-11-20T09:00:00Z", end_at: "2026-11-20T12:00:00Z" }] }, { time_zone: "UTC" });
  it("sorts, marks Now and Next", () => {
    const l = label(rows, now);
    expect(l.map((r) => [r.title, !!r.now, !!r.next])).toEqual([["Build", true, false], ["Demos", false, true]]);
  });
  it("nudges for a row starting soon", () => {
    expect(nudgeLine(rows, { time_zone: "UTC" }, now)).toEqual(["Demos starts in 10 minutes.", "Build is on now."]);
  });
});

describe("words", () => {
  it("counts down like the old app", () => {
    expect(countdownText(3 * 86400000)).toBe("3 days");
    expect(countdownText(90061000)).toBe("1 d 1 h 1 m");
    expect(countdownText(65000)).toBe("1 m 05 s");
  });
  it("reads scores and places", () => {
    expect(scoreText(81.25)).toBe("81.25");
    expect(scoreText(100)).toBe("100");
    expect(headline({ team_name: "x", rank: 2, tied: true }, 5)).toBe("Joint 2nd of 5 judged teams");
    expect(headline({ team_name: "x", rank: 11 }, null)).toBe("11th");
  });
  it("splits a need into a title and a line", () => {
    expect(splitNeed({ kind: "news", text: "Your site was taken down. It broke the rules." })).toEqual(["Your site was taken down.", "It broke the rules."]);
  });
});
