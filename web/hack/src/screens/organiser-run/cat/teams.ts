// Teams (organiser) in every state: both sub-pages, Fill teams, placing one
// person, messaging selected teams, and the read-only cases.
import { refuse, type Entry } from "@kit/catalogue/harness";
import type { Team } from "../data-teams";
import { E, org, orgApi, url } from "./fx";

const m = (user_id: string, display_name: string, admin = false) => ({ user_id, display_name, admin, email: user_id + "@example.com" });
export const teams: Team[] = [
  { id: "t1", slug: "lumbung", name: "Lumbung", code: "LMB-221", link: "https://simple-hack.app/team/LMB-221", locked: false, track: { slug: "food", name: "Food" }, members: [m("u-siti", "Siti Rahma", true), m("u-ayu", "Ayu"), m("u-nadia", "Nadia")] },
  { id: "t2", slug: "kopi-kode", name: "Kopi Kode", code: "KPK-118", link: "https://simple-hack.app/team/KPK-118", locked: true, track: { slug: "climate", name: "Climate" }, members: [m("u-budi", "Budi", true), m("u-rina", "Rina", true), m("u-tono", "Tono"), m("u-wati", "Wati")] },
  { id: "t3", slug: "ojek-ai", name: "Ojek AI", code: "OJK-903", link: "https://simple-hack.app/team/OJK-903", track: null, members: [m("u-dewi", "Dewi")] },
];
export const solo = [
  { user_id: "u-agus", display_name: "Agus Santoso", email: "agus@example.com" },
  { user_id: "u-lina", display_name: "Lina", email: "lina@example.com" },
];
export const list = { teams, team_size_max: 4, on_no_team: solo };
const open = { solo: [{ user_id: "u-agus", intro: "Front-end, some Figma" }] };
const plan = {
  plan: [
    { team: "ojek-ai", name: "Ojek AI", members: [{ user_id: "u-agus", display_name: "Agus Santoso" }] },
    { team: "team-4", name: "Team 4", new: true, members: [{ user_id: "u-lina", display_name: "Lina" }] },
  ],
  skipped: [{ name: "Kopi Kode" }],
  size: 4,
  applied: false,
};

const T = E("teams");
const ok = (o: Record<string, unknown> = {}, ev?: ReturnType<typeof org>) => orgApi({ ["GET " + T]: list, ["GET " + E("teams/open")]: open, ["POST " + E("teams/fill")]: plan, ...o }, ev);
const teamsTab = url("teams", "&tab=teams");
const soloTab = url("teams", "&tab=solo");

export const entries: Entry[] = [
  { id: "organiser-teams.loading", title: "Teams: loading", url: teamsTab, api: ok({ ["GET " + T]: "pending" }) },
  { id: "organiser-teams.error", title: "Teams: the list did not load", url: teamsTab, api: ok({ ["GET " + T]: refuse(500, "error", "Something went wrong. Try again.") }) },
  { id: "organiser-teams.empty", title: "Teams: no teams yet", url: teamsTab, api: ok({ ["GET " + T]: { teams: [], team_size_max: 4, on_no_team: solo } }) },
  { id: "organiser-teams.loaded", title: "Teams: three teams, one locked, one full", url: url("teams"), api: ok() },
  {
    id: "organiser-teams.message-selected",
    title: "Teams: two teams picked, Message selected",
    url: teamsTab,
    api: ok(),
    steps: [{ click: "[aria-label='Select Lumbung to message']" }, { click: "[aria-label='Select Ojek AI to message']" }, { click: "text=Message selected (2)" }],
  },
  {
    id: "organiser-teams.message-empty",
    title: "Teams: send with no message written",
    url: teamsTab,
    api: ok(),
    steps: [{ click: "[aria-label='Select Lumbung to message']" }, { click: "text=Message selected" }, { click: "text=Send" }],
  },
  { id: "organiser-teams.team-finding-off", title: "Teams: team finding is off", url: teamsTab, api: ok({}, org({}, { team_finding: false })) },
  { id: "organiser-teams.solo", title: "Not on a team: two people, one looking", url: soloTab, api: ok() },
  { id: "organiser-teams.everyone-placed", title: "Not on a team: everyone is placed", url: soloTab, api: ok({ ["GET " + T]: { ...list, on_no_team: [] } }) },
  { id: "organiser-teams.fill-teams-preview", title: "Fill teams: the preview", url: soloTab, api: ok(), steps: [{ click: "text=Fill teams" }] },
  {
    id: "organiser-teams.fill-teams-size",
    title: "Fill teams: a size out of range",
    url: soloTab,
    api: ok(),
    steps: [{ click: "text=Fill teams" }, { fill: ["#fill-size", "9"] }],
  },
  { id: "organiser-teams.fill-teams-nobody", title: "Fill teams: nobody left to place", url: soloTab, api: ok({ ["POST " + E("teams/fill")]: { plan: [], skipped: [], size: 4 } }), steps: [{ click: "text=Fill teams" }] },
  {
    id: "organiser-teams.place",
    title: "Not on a team: put one person on a team",
    url: soloTab,
    api: ok(),
    steps: [{ click: "[aria-label='More for Agus Santoso']" }, { click: "text=Put on a team…" }],
  },
  { id: "organiser-teams.ended", title: "Teams: the event has ended", url: teamsTab, api: ok({}, org({}, { stage: "archived" })) },
  { id: "organiser-teams.ended-solo", title: "Not on a team: the event has ended", url: soloTab, api: ok({}, org({}, { stage: "archived" })) },
  { id: "organiser-teams.admin-view", title: "Teams: a platform admin's read-only look", url: teamsTab, api: ok({}, org({ admin_view: true })) },
];
