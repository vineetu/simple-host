import { describe, expect, it } from "vitest";
import {
  chooseStep,
  cueHeading,
  headsUp,
  judgingPoolLine,
  nextStepContent,
  savedPlace,
  scoresChangedSincePublish,
  setupLines,
  stageChoices,
  stageEffect,
  suggestedAnnouncement,
  ties,
  type OrgView,
} from "./data-overview";

const view = (stage: string, o: Partial<OrgView> = {}, ev: Record<string, unknown> = {}) => ({ event: { slug: "g", title: "G", stage, ...ev }, role: "organiser", ...o }) as OrgView;

describe("the next step", () => {
  it("says End is blocked with a Settings link when nobody joined", () => {
    const c = nextStepContent({ stage: "results" }, { kind: "stage", stage: "archived", label: "End event", blocked: "archive_needs_participants" }, "/s", "/e");
    expect(c.blocked).toBe(true);
    expect(c.link?.[1]).toBe("Settings");
  });
  it("publishes first at Results, and a changed snapshot means Publish again", () => {
    expect(chooseStep(view("results", { next_step: { kind: "stage", stage: "archived", label: "End event" } }), false)).toEqual({ kind: "publish_results", label: "Publish results" });
    expect(chooseStep(view("results", { next_step: { kind: "stage", stage: "archived" } }, { results_published: true }), true).label).toBe("Publish again");
  });
  it("turns the lock row into Finish judging", () => {
    expect(chooseStep(view("judging", { checklist: { next: { kind: "link", target_id: "o.lock", label: "Lock" } } }), false)).toMatchObject({ kind: "stage", stage: "results" });
  });
  it("asks from the schedule", () => {
    expect(cueHeading({ time_zone: "UTC" }, { kind: "stage", stage: "building", cue: { key: "row", title: "Kickoff", now: true } })).toBe("Kickoff is now. Start building?");
    expect(cueHeading({ time_zone: "UTC" }, { kind: "stage", stage: "closed", cue: { key: "entry_deadline", now: true } })).toBe("Entries are due now. Close entries?");
  });
  it("gives one heads-up", () => {
    const s = { kind: "stage", stage: "judging" };
    expect(headsUp(s, { pending: 0, onNoTeam: 0, judges: 0, noTrack: 2 })).toBe("No judges have joined yet. Share the judge link first.");
    expect(headsUp(s, { pending: 0, onNoTeam: 0, judges: 2, noTrack: 1 })).toBe("1 team with no track won't be judged until it picks one, or you assign one.");
    expect(headsUp({ kind: "stage", stage: "building" }, { pending: 1, onNoTeam: 0, judges: 0, noTrack: 0 })).toMatch(/^1 person is still waiting/);
  });
  it("counts the judging pool", () => {
    expect(judgingPoolLine({ judged: 3, no_entry: 1 })).toBe("3 teams will be judged. 1 team has no entry, so it won't be.");
    expect(judgingPoolLine({ judged: 0, excluded: 2 })).toBe("No team has an entry to judge yet. 2 teams you excluded stay out.");
  });
});

describe("change stage", () => {
  it("offers every other stage but archived, the next one first", () => {
    expect(stageChoices("building", undefined)).toEqual({ choices: ["draft", "open", "closed", "judging", "results"], initial: "closed" });
    expect(stageChoices("results", undefined).initial).toBe("judging");
  });
  it("says what a move back does", () => {
    expect(stageEffect("building", "draft")).toMatch(/join link stops working/);
    expect(stageEffect("draft", "open")).toMatch(/^Sign-up opens/);
  });
  it("summarises the judging setup and says when it is short", () => {
    const r = setupLines("g", { assignment_mode: "automatic", judges_per_team: 2, judges: [{ name: "A" }] }, { target: 2, min: 1, max: 2, short: [{ name: "T", have: 1 }] });
    expect(r.lines.map((l) => l[0])).toEqual(["Mode: Automatic", "Judges per team: 2", "Who judges: A", "Short: T (1 of 2)"]);
    expect(r.short).toBe(true);
  });
});

describe("results", () => {
  const pub = [
    { team_id: "a", total: 9, rank: 1 },
    { team_id: "b", total: 7, rank: 2, tie_decider: "organiser" },
    { team_id: "c", total: 7, rank: 3, tie_decider: "organiser" },
  ];
  it("sees changed totals, never a failed preview", () => {
    expect(scoresChangedSincePublish(pub, pub)).toBe(false);
    expect(scoresChangedSincePublish(pub, [...pub, { team_id: "d", total: 1 }])).toBe(true);
    expect(scoresChangedSincePublish(pub, [], false)).toBe(false);
  });
  it("keeps the places given to a tie that has not changed", () => {
    const live = [
      { team_id: "b", total: 7, rank: 2, tied: true },
      { team_id: "c", total: 7, rank: 2, tied: true },
    ];
    expect(ties(live)).toEqual([[2, live]]);
    expect(savedPlace(live[1], live, pub)).toBe(3);
    expect(savedPlace({ ...live[1], total: 8 }, live, pub)).toBeNull();
  });
  it("suggests an announcement for each move", () => {
    expect(suggestedAnnouncement("publish_results")?.title).toBe("Results are out");
    expect(suggestedAnnouncement("draft", true)?.title).toBe("Sign-up is paused");
    expect(suggestedAnnouncement("results", true)).toBeNull();
  });
});
