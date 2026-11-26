// Judging in every state (atlas organiser-judging): setup before judging,
// judging open, locked, a judge who left, Panel teams with no track.
import { refuse, type Entry } from "@kit/catalogue/harness";
import { E, manage, orgApi } from "./fixtures";

const url = manage("judging");
const judges = [
  { user_id: "u-budi", name: "Pak Budi", email: "budi@example.com" },
  { user_id: "u-dewi", name: "Dewi Lestari", email: "dewi@example.com" },
  { user_id: "u-siti", name: "Siti Rahma", email: "siti@example.com" },
];
const teams = [
  { id: "tm-1", slug: "codebloom", name: "CodeBloom", track: { id: "t1", name: "Food waste" } },
  { id: "tm-2", slug: "shebuilds", name: "SheBuilds", track: { id: "t2", name: "Energy" } },
  { id: "tm-3", slug: "nusatech", name: "NusaTech", track: null },
];
const settings = (o: Record<string, unknown> = {}) => ({ assignment_mode: "manual", judges_per_team: 2, judges, ...o });
const dashboard = {
  teams: [
    { team_id: "tm-1", team_name: "CodeBloom", judges_scored: 2, judges_eligible: 2 },
    { team_id: "tm-2", team_name: "SheBuilds", judges_scored: 1, judges_eligible: 2 },
    { team_id: "tm-3", team_name: "NusaTech", judges_scored: 0, judges_eligible: 1 },
  ],
  judges: [
    { judge_id: "u-budi", judge_name: "Pak Budi", done_count: 3, assigned_count: 3 },
    { judge_id: "u-dewi", judge_name: "Dewi Lestari", done_count: 1, assigned_count: 2 },
  ],
  former_judges: [],
};
const former = (assigned: number) => ({ ...dashboard, former_judges: [{ user_id: "u-old", display_name: "Rina Hartono", counted_teams: 2, assigned_count: assigned }] });
const conflicts = [{ team_id: "tm-1", judge_id: "u-dewi", team_name: "CodeBloom", judge_name: "Dewi Lestari" }];
const base = (o: Record<string, unknown> = {}) => ({
  ["GET " + E + "/judging/settings"]: settings(),
  ["GET " + E + "/conflicts"]: { conflicts },
  ["GET " + E + "/teams"]: { teams },
  ["GET " + E + "/assignments"]: { assignments: [{ judge_id: "u-budi", team_id: "tm-1" }] },
  ["GET " + E + "/judging/dashboard"]: dashboard,
  ...o,
});
const judgingStage = { stage: "judging", scoring_open: true };

export const entries: Entry[] = [
  { id: "organiser-judging.loading", title: "Judging: loading", url, api: orgApi(base({ ["GET " + E + "/judging/settings"]: "pending" })) },
  { id: "organiser-judging.error", title: "Judging: did not load", url, api: orgApi(base({ ["GET " + E + "/teams"]: refuse(500, "error", "Something went wrong. Try again.") })) },
  { id: "organiser-judging.setup", title: "Judging: setting up before judging (Manual)", url, api: orgApi(base()) },
  {
    id: "organiser-judging.empty",
    title: "Judging: no teams, judges or conflicts yet",
    url,
    api: orgApi(
      base({
        ["GET " + E + "/judging/settings"]: settings({ judges: [] }),
        ["GET " + E + "/conflicts"]: { conflicts: [] },
        ["GET " + E + "/teams"]: { teams: [] },
        ["GET " + E + "/assignments"]: { assignments: [] },
        ["GET " + E + "/judging/dashboard"]: { teams: [], judges: [], former_judges: [] },
      }),
    ),
  },
  {
    id: "organiser-judging.automatic-not-generated",
    title: "Judging: Automatic, assignments not generated yet",
    url,
    api: orgApi(base({ ["GET " + E + "/judging/settings"]: settings({ assignment_mode: "automatic" }), ["GET " + E + "/assignments"]: { assignments: [] } }), { stage: "closed" }),
  },
  { id: "organiser-judging.judging", title: "Judging: judging open, the organiser judges too", url, api: orgApi(base(), judgingStage, { can: { manage: true, judge: true } }) },
  { id: "organiser-judging.panel-untracked", title: "Judging: Panel mode, a team with no track", url, api: orgApi(base({ ["GET " + E + "/judging/settings"]: settings({ assignment_mode: "panel" }) }), judgingStage) },
  { id: "organiser-judging.former-judge", title: "Judging: a judge left the event", url, api: orgApi(base({ ["GET " + E + "/judging/dashboard"]: former(3) }), judgingStage) },
  {
    id: "organiser-judging.delete-scores",
    title: "Judging: deleting a former judge's scores asks for the address name",
    url,
    api: orgApi(base({ ["GET " + E + "/judging/dashboard"]: former(3) }), judgingStage),
    steps: [{ click: "text=Delete scores" }],
  },
  { id: "organiser-judging.locked", title: "Judging: locked at Results, publish from Home", url, api: orgApi(base({ ["GET " + E + "/judging/dashboard"]: former(1) }), { stage: "results", judging_locked_at: "2026-11-19T12:00:00Z" }) },
  {
    id: "organiser-judging.add-refused",
    title: "Judging: adding a conflict that is refused",
    url,
    api: orgApi(base({ ["POST " + E + "/conflicts"]: refuse(409, "conflict_exists", "Dewi Lestari already has a conflict with CodeBloom.") })),
    steps: [{ select: ["#cf-judge", "u-dewi"] }, { click: "text=Add conflict" }],
  },
  { id: "organiser-judging.ended", title: "Judging: the event has ended", url, api: orgApi(base(), { stage: "archived" }) },
  { id: "organiser-judging.admin", title: "Judging: a platform admin inspecting", url, api: orgApi(base(), {}, { admin_view: true }) },
];
