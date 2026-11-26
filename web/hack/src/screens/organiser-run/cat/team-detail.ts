// Team detail (organiser) in every state: a team as it runs, locked, kept
// after everyone left, full, read only, and every dialog.
import { refuse, type Entry } from "@kit/catalogue/harness";
import type { Team } from "../data-teams";
import { E, org, orgApi, url } from "./fx";
import { list, teams } from "./teams";

const tracks = [
  { slug: "food", name: "Food" },
  { slug: "climate", name: "Climate" },
];
const withTracks = (ev: Record<string, unknown> = {}, o: Record<string, unknown> = {}) => org(o, { tracks, ...ev });
const messages = {
  messages: [
    { id: "m2", text: "Can we move our demo slot to the afternoon? Two of us are on a train until 1 pm.", sender_name: "Siti Rahma", from_team: true, created_at: "2026-11-20T07:00:00Z" },
    { id: "m1", text: "Welcome! Your mentor is Pak Hendra, find him by the stage.", sender_name: "Green Campus team", from_team: false, created_at: "2026-11-18T03:15:00Z" },
  ],
};
const entriesList = { required: ["title", "one_of"], entries: [{ team: { slug: "lumbung" }, missing: ["one_of"] }, { team: { slug: "kopi-kode" }, missing: [] }] };
const people = teams.flatMap((t) => t.members.map((m) => ({ user_id: m.user_id, email: m.email })));
const withTeams = (ts: Team[], o: Record<string, unknown> = {}) => ({ ...list, teams: ts, ...o });

const ok = (o: Record<string, unknown> = {}, ev = withTracks()) =>
  orgApi(
    {
      ["GET " + E("teams")]: list,
      ["GET " + E("people")]: people,
      ["GET " + E("entries")]: entriesList,
      ["GET " + E("judging/settings")]: { assignment_mode: "automatic" },
      ["GET " + E("teams/lumbung/messages")]: messages,
      ["GET " + E("teams/kopi-kode/messages")]: { messages: [] },
      ["GET " + E("teams/ojek-ai/messages")]: { messages: [] },
      ...o,
    },
    ev,
  );
const lumbung = url("team", "&team=lumbung");
const kopi = url("team", "&team=kopi-kode");
const ojek = url("team", "&team=ojek-ai");
const menu = "[aria-label='More for Ayu']";

export const entries: Entry[] = [
  { id: "organiser-team-detail.loading", title: "Team detail: loading", url: lumbung, api: ok({ ["GET " + E("teams")]: "pending" }) },
  { id: "organiser-team-detail.error", title: "Team detail: did not load", url: lumbung, api: ok({ ["GET " + E("teams")]: refuse(500, "error", "Something went wrong. Try again.") }) },
  { id: "organiser-team-detail.not-found", title: "Team detail: no such team", url: url("team", "&team=gone"), api: ok() },
  { id: "organiser-team-detail.loaded", title: "Team detail: a team with messages and an entry missing a field", url: lumbung, api: ok() },
  { id: "organiser-team-detail.no-track", title: "Team detail: no track yet, the track picker", url: ojek, api: ok() },
  { id: "organiser-team-detail.locked", title: "Team detail: locked (and full)", url: kopi, api: ok() },
  {
    id: "organiser-team-detail.kept",
    title: "Team detail: kept after everyone left",
    url: kopi,
    api: ok({ ["GET " + E("teams")]: withTeams([...teams.slice(0, 1), { ...teams[1], members: [], kept: true, locked: true }, teams[2]]) }, withTracks({ stage: "judging" })),
  },
  { id: "organiser-team-detail.full", title: "Team detail: full, Add someone off", url: kopi, api: ok({ ["GET " + E("teams")]: withTeams([teams[0], { ...teams[1], locked: false }, teams[2]]) }) },
  { id: "organiser-team-detail.everyone-placed", title: "Team detail: nobody left to add", url: lumbung, api: ok({ ["GET " + E("teams")]: withTeams(teams, { on_no_team: [] }) }) },
  { id: "organiser-team-detail.ended", title: "Team detail: the event has ended", url: lumbung, api: ok({}, withTracks({ stage: "archived" })) },
  { id: "organiser-team-detail.admin-view", title: "Team detail: a platform admin's read-only look", url: lumbung, api: ok({}, withTracks({}, { admin_view: true })) },
  { id: "organiser-team-detail.add", title: "Team detail: add someone", url: lumbung, api: ok(), steps: [{ click: "text=+ Add someone" }] },
  { id: "organiser-team-detail.row-menu", title: "Team detail: a member's menu", url: lumbung, api: ok(), steps: [{ click: menu }] },
  { id: "organiser-team-detail.move", title: "Team detail: move to another team", url: lumbung, api: ok(), steps: [{ click: menu }, { click: "text=Move to another team…" }] },
  {
    id: "organiser-team-detail.move-no-room",
    title: "Team detail: move, but no other team has room",
    url: lumbung,
    api: ok({ ["GET " + E("teams")]: withTeams([teams[0], teams[1], { ...teams[2], members: teams[1].members.map((m) => ({ ...m, user_id: m.user_id + "x" })) }]) }),
    steps: [{ click: menu }, { click: "text=Move to another team…" }],
  },
  { id: "organiser-team-detail.remove", title: "Team detail: take someone off, the confirm", url: lumbung, api: ok(), steps: [{ click: menu }, { click: "text=Take off team" }] },
  { id: "organiser-team-detail.made-admin", title: "Team detail: made admin, with Undo", url: lumbung, api: ok(), steps: [{ click: menu }, { click: "text=Make admin" }] },
  { id: "organiser-team-detail.rename-empty", title: "Team detail: rename with no name", url: lumbung, api: ok(), steps: [{ fill: ["#rename-name", " "] }, { click: "form:has(#rename-name) button[type=submit]" }] },
  { id: "organiser-team-detail.new-link", title: "Team detail: a new team link, the confirm", url: lumbung, api: ok(), steps: [{ click: "text=New link" }] },
  { id: "organiser-team-detail.delete", title: "Team detail: delete the team, the confirm", url: lumbung, api: ok(), steps: [{ click: "text=Delete team" }] },
  { id: "organiser-team-detail.message", title: "Team detail: message the team", url: lumbung, api: ok(), steps: [{ click: "text=Message team" }] },
  { id: "organiser-team-detail.message-empty", title: "Team detail: send with no message", url: lumbung, api: ok(), steps: [{ click: "text=Message team" }, { click: "text=Send" }] },
  {
    id: "organiser-team-detail.message-refused",
    title: "Team detail: a message the server refuses keeps the dialog open with its words",
    url: lumbung,
    api: ok({ ["POST " + E("team-messages")]: refuse(429, "rate_limited", "You've sent a lot of messages in the last hour. Wait a few minutes and try again.") }),
    steps: [{ click: "text=Message team" }, { fill: ["#tm-text", "Your demo slot moved to 2 pm."] }, { click: "text=Send" }],
  },
];
