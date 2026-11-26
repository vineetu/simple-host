import { describe, expect, it } from "vitest";
import type { EventDetail } from "@/lib/client";
import { NO_FINDING, firstWay, inviteLine, justPlaced, openLine, placedNote, rules, tabsFor, teamLink, ways, type Finding } from "./data";
import { ago, say } from "./words";
import { ApiError } from "@kit/api/call";

const NOW = Date.parse("2026-11-20T10:00:00Z");
const d = (stage: string, team: unknown = null, extra: Record<string, unknown> = {}) =>
  ({ event: { slug: "x", title: "X", stage, team_size_max: 3, ...extra }, role: "participant", me: { team } }) as unknown as EventDetail;
const f = (o: Partial<Finding> = {}): Finding => ({ ...NO_FINDING, enabled: true, ...o });

class Mem {
  m = new Map<string, string>();
  getItem(k: string) {
    return this.m.has(k) ? this.m.get(k)! : null;
  }
  setItem(k: string, v: string) {
    this.m.set(k, v);
  }
  removeItem(k: string) {
    this.m.delete(k);
  }
}

describe("Team rules and ways", () => {
  it("opens every way while teams are open, and none once closed", () => {
    expect(ways(rules(d("open"), NOW), f())).toEqual(["ask", "code", "start"]);
    expect(ways(rules(d("closed"), NOW), f())).toEqual([]);
    expect(ways(rules(d("open"), NOW), NO_FINDING)).toEqual(["code", "start"]);
  });
  it("keeps Join with a code after the deadline, before Closed", () => {
    expect(ways(rules(d("building", null, { submission_deadline: "2026-11-19T00:00:00Z" }), NOW), f())).toEqual(["code"]);
  });
  it("picks Ask when a team is looking or a request is out, else Start; Join when finding is off", () => {
    expect(firstWay(["ask", "code", "start"], f({ teams: [{ slug: "a", name: "A", member_count: 1, team_size_max: 3 }] }))).toBe("ask");
    expect(firstWay(["ask", "code", "start"], f({ pending: [{ id: "1", kind: "request" }] }))).toBe("ask");
    expect(firstWay(["ask", "code", "start"], f())).toBe("start");
    expect(firstWay(["code", "start"], NO_FINDING)).toBe("code");
  });
  it("knows a full team and the admin", () => {
    const r = rules(d("building", { slug: "t", name: "T", admin: true, members: [{}, {}, {}] }), NOW);
    expect(r.full).toBe(true);
    expect(r.isAdmin).toBe(true);
    expect(r.canAct).toBe(true);
    expect(rules(d("building", { slug: "t", name: "T", frozen: true }), NOW).canAct).toBe(false);
  });
  it("shows Find people only with team finding on", () => {
    expect(tabsFor(NO_FINDING)).toEqual(["members", "settings"]);
    expect(tabsFor(f())).toEqual(["members", "people", "settings"]);
  });
});

describe("Team lines", () => {
  const t = { slug: "a", name: "A", member_count: 2, team_size_max: 4, track: { name: "Food" }, members: ["Dewi", "Rina"], looking_for: "a designer" };
  it("says who an invite is from and what a looking team needs", () => {
    expect(inviteLine({ id: "1", kind: "invite", team: "a" }, f({ teams: [t] }))).toBe("Invited you · 2 of 4 · Food");
    expect(inviteLine({ id: "1", kind: "invite", team: "zz" }, f())).toBe("Invited you to join");
    expect(openLine(t)).toBe("2 of 4 · Food · Dewi, Rina · Looking for: a designer");
    expect(openLine({ ...t, full: true })).toBe("2 of 4 · Food · Dewi, Rina");
  });
  it("builds the team link from the code when the server sends none", () => {
    expect(teamLink({ slug: "a", name: "A", code: "k7m2p9qa" }, "https://hack.test")).toEqual({ link: "https://hack.test/team/k7m2p9qa", addr: "hack.test/team/k7m2p9qa" });
  });
});

describe("Just placed", () => {
  it("says joined after the person's own join, placed after a change they did not make, nothing the first time", () => {
    const store = { local: new Mem() as unknown as Storage, session: new Mem() as unknown as Storage };
    expect(justPlaced("u", "ev", "t1", store)).toBe("");
    expect(justPlaced("u", "ev", "t2", store)).toBe("placed");
    store.session.setItem("sh-selfjoin-ev", "joined");
    expect(justPlaced("u", "ev", "t3", store)).toBe("joined");
    expect(justPlaced("u", "ev", "t3", store)).toBe("joined"); // the same page load keeps it
    expect(placedNote("started", "Kopi")).toBe("You started Kopi. Copy its link to bring people in.");
  });
});

describe("Words", () => {
  it("prefers the screen's own sentence for a known code", () => {
    expect(say(new ApiError(404, "team_not_found", "team not found"))).toBe("That team code does not match a team.");
    expect(say(new ApiError(400, "odd", "Server says no."))).toBe("Server says no.");
  });
  it("says how long ago", () => {
    expect(ago("2026-11-20T09:56:00Z", NOW)).toBe("4 minutes ago");
    expect(ago("2026-11-18T10:00:00Z", NOW)).toBe("2 days ago");
    expect(ago("2026-11-21T10:00:00Z", NOW)).toBe("");
  });
});
