// The assignment grid in every state (atlas organiser-judging-assignments).
import { refuse, type Entry } from "@kit/catalogue/harness";
import { E, manage, orgApi } from "./fixtures";

const url = manage("assignments");
const judges = [
  { user_id: "u-budi", name: "Pak Budi" },
  { user_id: "u-dewi", name: "Dewi Lestari" },
  { user_id: "u-siti", name: "Siti Rahma" },
];
const teams = [
  { id: "tm-1", slug: "codebloom", name: "CodeBloom" },
  { id: "tm-2", slug: "shebuilds", name: "SheBuilds" },
  { id: "tm-3", slug: "nusatech", name: "NusaTech" },
];
const grid = (mode: string, o: Record<string, unknown> = {}) => ({
  ["GET " + E + "/judging/settings"]: { assignment_mode: mode, judges_per_team: 2, judges },
  ["GET " + E + "/teams"]: { teams },
  ["GET " + E + "/conflicts"]: { conflicts: [{ team_id: "tm-1", judge_id: "u-dewi", team_name: "CodeBloom", judge_name: "Dewi Lestari" }] },
  ["GET " + E + "/assignments"]: { assignments: [{ judge_id: "u-budi", team_id: "tm-1" }, { judge_id: "u-siti", team_id: "tm-2" }] },
  ["GET " + E + "/judging/panels"]: { panels: [{ judge_id: "u-budi", track_id: "t1" }, { judge_id: "u-dewi", track_id: "t1" }] },
  ...o,
});
const closed = { stage: "closed" };

export const entries: Entry[] = [
  { id: "organiser-judging-assignments.loading", title: "Assignment grid: loading", url, api: orgApi(grid("manual", { ["GET " + E + "/judging/panels"]: "pending" }), closed) },
  { id: "organiser-judging-assignments.error", title: "Assignment grid: did not load", url, api: orgApi(grid("manual", { ["GET " + E + "/assignments"]: refuse(500, "error", "Something went wrong. Try again.") }), closed) },
  { id: "organiser-judging-assignments.manual", title: "Assignment grid: Manual, a conflicted pair dimmed", url, api: orgApi(grid("manual"), closed) },
  { id: "organiser-judging-assignments.manual-dirty", title: "Assignment grid: Manual, a tick not saved yet", url, api: orgApi(grid("manual"), closed), steps: [{ click: "label:has(input:not(:checked):not(:disabled))" }] },
  { id: "organiser-judging-assignments.panel", title: "Assignment grid: Panel, conflicts not dimmed", url, api: orgApi(grid("panel"), closed) },
  { id: "organiser-judging-assignments.wrong-mode", title: "Assignment grid: the Panel grid while the mode is Manual", url: url + "&grid=panel", api: orgApi(grid("manual"), closed) },
  { id: "organiser-judging-assignments.empty", title: "Assignment grid: no teams or judges yet", url, api: orgApi(grid("manual", { ["GET " + E + "/teams"]: { teams: [] } }), closed) },
  { id: "organiser-judging-assignments.started", title: "Assignment grid: judging has started", url, api: orgApi(grid("manual"), { stage: "judging", scoring_open: true }) },
  {
    id: "organiser-judging-assignments.save-refused",
    title: "Assignment grid: the save is refused",
    url,
    api: orgApi(grid("manual", { ["PUT " + E + "/assignments"]: refuse(409, "judging_setup_locked", "Judging has started. Move the event back to Entries closed to change this.") }), closed),
    steps: [{ click: "label:has(input:not(:checked):not(:disabled))" }, { click: "text=Save judging access" }],
  },
];
