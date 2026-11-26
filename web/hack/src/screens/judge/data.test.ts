import { describe, expect, it } from "vitest";
import type { EventDetail } from "@/lib/client";
import {
  embedURL,
  emptyToScore,
  framedURL,
  guardLine,
  homeTop,
  knownList,
  leaveLine,
  onJudgeDocument,
  openTab,
  pickTeam,
  setupStep,
  siteHref,
  walkOrder,
  weightPct,
  type QueueRow,
} from "./data";
import { splitNeed } from "@/shared/NeedsYou";

const ev = (o: Partial<EventDetail["event"]> = {}) => ({ slug: "x", title: "X", stage: "judging", scoring_open: true, ...o }) as EventDetail["event"];
const row = (id: string, o: Partial<QueueRow> = {}) => ({ team_id: id, team: { slug: id, name: id.toUpperCase() }, ...o }) as QueueRow;

describe("Overview's lead", () => {
  it("follows the old order of checks", () => {
    expect(homeTop(ev({ results_published: true, scoring_open: false }), { queue: [] }).kind).toBe("results");
    expect(homeTop(ev({ scoring_open: false }), { queue: [row("a")] }).kind).toBe("not-open");
    expect(homeTop(ev(), { queue: [] }).kind).toBe("nothing");
    expect(homeTop(ev(), { queue: [], candidates: [{ team_id: "a", team_name: "A", conflicted: true }] }).kind).toBe("all-conflicted");
    expect(homeTop(ev({ judging_locked_at: "2026-11-20T00:00:00Z" }), { queue: [row("a", { done: true })] }).kind).toBe("locked-done");
    expect(homeTop(ev({ judging_locked_at: "2026-11-20T00:00:00Z" }), { queue: [row("a"), row("b")] })).toEqual({ kind: "locked-partial", scored: 0, total: 2, left: 2 });
    expect(homeTop(ev(), { queue: [row("a", { done: true })] }).kind).toBe("done");
    expect(homeTop(ev(), { queue: [row("a", { done: true }), row("b")] })).toEqual({ kind: "next", scored: 1, total: 2, team: "b" });
  });
  it("puts a setup step before scoring, never the scoring step itself", () => {
    const items = [{ id: "j.rubric", label: "Read the rubric", detail: "3 criteria" }];
    expect(setupStep({ items, next: { kind: "link", item_id: "j.rubric", label: "Read the rubric", link: { href: "/e/x/judge?s=rubric" } } })).toMatchObject({ id: "j.rubric", href: "/e/x/judge?s=rubric", detail: "3 criteria" });
    expect(setupStep({ items, next: { kind: "link", item_id: "j.scores", link: { href: "/e/x/judge?s=queue" } } })).toBeNull();
    expect(setupStep({ items, next: null })).toBeNull();
  });
  it("says what leaving does", () => {
    expect(leaveLine({ scored_teams: 4 }, false).text).toBe("You'll lose access to judging. Your scores for 4 teams stay and still count in the results.");
    expect(leaveLine({ scored_teams: 1 }, false).text).toContain("1 team stay");
    expect(leaveLine({}, false).text).toBe("You'll lose access to judging.");
    expect(leaveLine({ blocked: { code: "event_closed" } }, false)).toEqual({ disabled: true, text: "This event has ended, so nobody can leave it. It stays on your Home as a record." });
    expect(leaveLine({}, true).text).toContain("as an organiser");
  });
  it("splits a Needs you line", () => {
    expect(splitNeed({ kind: "judging_open", text: "Judging is open. Score your teams.", section: "queue" })).toEqual(["Judging is open.", "Score your teams."]);
    expect(splitNeed({ kind: "judging_open", text: "Judging is open", section: "queue" })).toEqual(["Judging is open", "Open My teams"]);
  });
});

describe("My teams", () => {
  it("says why To score is empty", () => {
    expect(emptyToScore({ queue: [row("a", { done: true })] }, 1)).toMatch(/^Nothing left/);
    expect(emptyToScore({ queue: [], candidates: [{ team_id: "a", team_name: "A" }] }, 0)).toMatch(/know someone on every team/);
    expect(emptyToScore({ queue: [], assignment_mode: "assigned" }, 0)).toBe("Nothing to score yet. No team with an entry is on your list.");
    expect(emptyToScore({ queue: [], assignment_mode: "open" }, 0)).toBe("Nothing to score yet. No team has an entry to judge.");
  });
  it("lists candidates and own conflicts once each", () => {
    const list = knownList({ queue: [], candidates: [{ team_id: "a", team_name: "A" }] }, [{ team_id: "a", team_name: "A", had_score: true }, { team_id: "b", team_name: "B" }]);
    expect(list.map((c) => [c.team_id, !!c.mine])).toEqual([
      ["a", true],
      ["b", true],
    ]);
    expect(knownList({ queue: [row("q")] }, []).map((c) => c.team_name)).toEqual(["Q"]);
  });
  it("opens the asked tab, else the first with something in it", () => {
    expect(openTab("scored", { "to-score": 1, scored: 0, conflicts: 0 })).toBe("scored");
    expect(openTab(null, { "to-score": 0, scored: 2, conflicts: 0 })).toBe("scored");
    expect(openTab("nope", { "to-score": 0, scored: 0, conflicts: 0 })).toBe("to-score");
  });
  it("opens the pinned snapshot over the live site, and only http(s)", () => {
    expect(siteHref(row("a", { pinned_url: "https://a.x.app/v/3", site_exists: true, site_url: "https://a.x.app/" }))).toBe("https://a.x.app/v/3");
    expect(siteHref(row("a", { site_exists: true, site_url: "https://a.x.app/" }))).toBe("https://a.x.app/");
    expect(siteHref(row("a", { site_exists: false, site_url: "https://a.x.app/" }))).toBe("");
    expect(siteHref(row("a", { pinned_url: "javascript:alert(1)" }))).toBe("");
  });
});

describe("Scoring", () => {
  it("keeps the judge's own walking order", () => {
    expect(walkOrder(["b", "gone", "a"], [row("a"), row("b"), row("c")])).toEqual(["b", "a", "c"]);
    expect(walkOrder("junk", [row("a")])).toEqual(["a"]);
  });
  it("opens the asked team, else the first unscored", () => {
    expect(pickTeam("b", [row("a"), row("b")])).toBe("b");
    expect(pickTeam("zz", [row("a", { done: true }), row("b")])).toBe("b");
    expect(pickTeam("", [])).toBe("");
  });
  it("shows weights as shares", () => {
    const all = [
      { id: "1", name: "A", max_points: 10, weight: 40 },
      { id: "2", name: "B", max_points: 10, weight: 60 },
    ];
    expect(weightPct(all[0], all)).toBe("40%");
    expect(weightPct({ id: "3", name: "C", max_points: 10 }, [{ id: "3", name: "C", max_points: 10 }])).toBe("");
  });
  it("names the unscored criteria in one line", () => {
    expect(guardLine(["Design"])).toBe("Design has no score yet. Press again to move on.");
    expect(guardLine(["Idea", "Build", "Design"])).toBe("Idea, Build and Design have no score yet. Press again to move on.");
  });
  it("frames only the four players and the site's own https preview", () => {
    expect(embedURL(row("a", { video: { url: "https://youtu.be/x", embed_url: "https://www.youtube-nocookie.com/embed/x" } }))).toBe("https://www.youtube-nocookie.com/embed/x");
    expect(embedURL(row("a", { video: { url: "https://youtu.be/x", embed_url: "https://evil.example/embed/x" } }))).toBe("");
    const site = { host: "a.x.simple-hack.app", frameable: true, preview_url: "https://a.x.simple-hack.app/_preview/t" };
    expect(framedURL(row("a", { site }))).toBe(site.preview_url);
    expect(framedURL(row("a", { site: { ...site, frameable: false } }))).toBe("");
    expect(framedURL(row("a", { site: { ...site, preview_url: "https://other.app/_preview/t" } }))).toBe("");
    expect(framedURL(row("a", { site: { ...site, preview_url: "http://a.x.simple-hack.app/" } }))).toBe("");
  });
  it("knows whether the page was loaded at a judge address", () => {
    expect(onJudgeDocument("https://simple-hack.app/e/x/judge?s=home")).toBe(true);
    expect(onJudgeDocument("https://simple-hack.app/events")).toBe(false);
    expect(onJudgeDocument("https://simple-hack.app/e/x/team?s=judge")).toBe(false);
    expect(onJudgeDocument(undefined)).toBe(true);
  });
});
