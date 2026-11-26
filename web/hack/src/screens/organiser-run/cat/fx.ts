// Fixtures for the organiser run screens: Green Campus Hack as its organiser
// sees it (GET E with the organiser block, the next step and the checklist),
// and api() with paths under the event shortened to E("...").
import type { Answer, Route } from "@kit/catalogue/harness";
import { api, detail } from "@/catalogue/fixtures";
import type { EventDetail } from "@/lib/client";

export const SLUG = "green-campus";
/** E is a path under the event: E("teams") is /v1/hack/events/green-campus/teams. */
export const E = (p = "") => "/v1/hack/events/" + SLUG + (p ? "/" + p : "");
export const url = (section: string, extra = "") => "/e/" + SLUG + "/manage?s=" + section + extra;

export type Org = EventDetail & Record<string, unknown>;

/** org is GET E as the organiser sees it; o overrides the top level, ev the event. */
export function org(o: Partial<Org> = {}, ev: Record<string, unknown> = {}): Org {
  const base = detail(SLUG, { me: { team: null } });
  return {
    ...base,
    event: {
      ...base.event,
      url: "https://green-campus.simple-hack.app/",
      starts_at: "2026-11-18T01:00:00Z",
      ends_at: "2026-11-23T00:00:00Z",
      submission_deadline: "2026-11-22T10:00:00Z",
      team_size_max: 4,
      team_finding: true,
      stages_offered: ["draft", "open", "building", "closed", "judging", "results", "archived"],
      ...ev,
    } as Org["event"],
    role: "organiser",
    can: { manage: true, judge: false, take_part: false },
    needs_you: [],
    organiser: {
      join_url: "https://simple-hack.app/join/GRN-4821",
      judge_url: "https://simple-hack.app/judge/JDG-7713",
      counts: { participants: 23, teams: 6, judges: 3, on_no_team: 2, emailable_participants: 21 } as Record<string, number>,
    },
    next_step: { kind: "stage", stage: "closed", label: "Close entries" },
    ...o,
  };
}

/** orgApi answers GET E with ev (org() by default) and over for anything else. */
export function orgApi(over: Record<string, Answer> = {}, ev: Org = org()): Route {
  return api({ ["GET " + E()]: ev, ...over });
}
