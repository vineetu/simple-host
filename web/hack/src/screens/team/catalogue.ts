// Team in every state: no team (each way in, invites, removed, closed,
// finding off, the walkthrough) and on a team (admin and member, each sub-page,
// locked, full, frozen, ended, just joined, and every dialog).
import { refuse, type Entry } from "@kit/catalogue/harness";
import { api, detail } from "@/catalogue/fixtures";
import type { EventDetail } from "@/lib/client";
import type { Finding, Member, Message, MyTeam } from "./data";

const SLUG = "jakarta-ai-night";
const url = `/e/${SLUG}/team?s=team`;
const E = "/v1/hack/events/" + SLUG;
const GET_E = "GET " + E;
const OPEN = E + "/teams/open";
const THREAD = E + "/team/messages";
const TRACKS = [
  { slug: "food", name: "Food" },
  { slug: "health", name: "Good Health" },
];

const base = detail(SLUG);
export function ev(stage: string, extra: Record<string, unknown> = {}): EventDetail["event"] {
  return { ...base.event, stage: stage as EventDetail["event"]["stage"], team_size_max: 4, tracks: TRACKS, submission_deadline: "2026-11-22T11:00:00Z", ...extra };
}
export function on(team: Partial<MyTeam> | null, stage = "building", meExtra: Record<string, unknown> = {}, evExtra: Record<string, unknown> = {}) {
  return detail(SLUG, {
    event: ev(stage, evExtra),
    role: "participant",
    can: { take_part: true },
    me: { user_id: "u-siti", approval_status: "approved", team: team ? ({ ...LUMBUNG, ...team } as MyTeam) : null, ...meExtra },
    needs_you: [],
  });
}

const members: Member[] = [
  { user_id: "u-siti", display_name: "Siti Rahma", admin: true, you: true },
  { user_id: "u-ayu", display_name: "Ayu", admin: false },
  { user_id: "u-nadia", display_name: "Nadia", admin: true },
];
export const LUMBUNG: MyTeam = {
  slug: "lumbung",
  name: "Lumbung",
  code: "k7m2p9qa",
  link: "https://simple-hack.app/team/k7m2p9qa",
  admin: true,
  locked: false,
  frozen: false,
  looking_for: "A designer",
  members,
  track: { slug: "food", name: "Food" },
  pending_requests: 1,
};

const finding: Finding = {
  enabled: true,
  teams: [
    { slug: "shebuilds", name: "SheBuilds", member_count: 2, team_size_max: 4, track: { name: "Good Health" }, members: ["Dewi", "Rina"], looking_for: "a designer" },
    { slug: "kopi-kode", name: "Kopi Kode", member_count: 4, team_size_max: 4, members: ["Bayu", "Tono", "Wati", "Eka"], full: true },
    { slug: "ojek-ai", name: "Ojek AI", member_count: 1, team_size_max: 4, track: { name: "Food" }, members: ["Dian"] },
  ],
  solo: [
    { user_id: "u-rizal", display_name: "Rizal", intro: "Full-stack, free to join anyone" },
    { user_id: "u-maya", display_name: "Maya", intro: "Designer, likes health ideas" },
  ],
  pending: [{ id: "rq-1", kind: "request", team: "ojek-ai", team_name: "Ojek AI" }],
  looking: true,
  intro: "Backend, Go and Postgres",
};
const onTeamFinding: Finding = {
  ...finding,
  pending: [
    { id: "rq-9", kind: "request", user_id: "u-bima", display_name: "Bima" },
    { id: "iv-2", kind: "invite", user_id: "u-maya", display_name: "Maya" },
  ],
};
const messages: Message[] = [
  { id: "m2", text: "Your demo slot is at 3 pm on Sunday. Bring a laptop with the site open.", created_at: "2026-11-20T08:00:00Z", sender_name: "Orga", from_team: false },
  { id: "m1", text: "Can we use an existing API key for the maps part?", created_at: "2026-11-19T12:30:00Z", sender_name: "Siti Rahma", from_team: true },
];

const noTeam = (o: Record<string, unknown> = {}) => api({ [GET_E]: on(null, "open"), [OPEN]: finding, ...o });
const withTeam = (team: Partial<MyTeam> = {}, o: Record<string, unknown> = {}, stage = "building") => api({ [GET_E]: on(team, stage), [OPEN]: onTeamFinding, [THREAD]: { messages }, ...o });
const tab = (t: string) => url + "&tab=" + t;
const radio = (name: string) => ({ click: `[role=radio][aria-label="${name}"]` });

const list: Entry[] = [
  { id: "participant-team.loading", title: "Team: loading", url, api: noTeam({ [OPEN]: "pending" }) },
  { id: "participant-team.no-team-ask", title: "Team: no team, Ask to join (one request out, one team full)", url, api: noTeam() },
  {
    id: "participant-team.invite-waiting",
    title: "Team: an invite waiting",
    url,
    api: noTeam({ [OPEN]: { ...finding, pending: [...finding.pending, { id: "iv-1", kind: "invite", team: "shebuilds", team_name: "SheBuilds" }] } }),
  },
  { id: "participant-team.join-code", title: "Team: no team, Join with a link or code", url, api: noTeam(), steps: [radio("Join with a link or code")] },
  {
    id: "participant-team.join-code-refused",
    title: "Team: the code matches no team",
    url,
    api: noTeam({ [`POST ${E}/teams/join`]: refuse(404, "team_not_found", "team not found") }),
    steps: [radio("Join with a link or code"), { fill: ["#join-code", "zz99zz99"] }, { click: "text=Join team" }],
  },
  { id: "participant-team.start", title: "Team: no team, Start a new team", url, api: noTeam(), steps: [radio("Start a new team")] },
  { id: "participant-team.start-empty", title: "Team: Start team with no name", url, api: noTeam(), steps: [radio("Start a new team"), { click: "text=Start team" }] },
  { id: "participant-team.finding-off", title: "Team: team finding off (two ways)", url, api: noTeam({ [GET_E]: on(null, "building"), [OPEN]: { enabled: false } }) },
  { id: "participant-team.closed", title: "Team: teams closed, no team", url, api: noTeam({ [GET_E]: on(null, "closed") }) },
  {
    id: "participant-team.removed",
    title: "Team: removed from a team",
    url,
    api: noTeam({ [GET_E]: on(null, "open", { team_removal_notice: "You were taken off Kopi Kode." }) }),
  },
  { id: "participant-team.walkthrough", title: "Team: the walkthrough (from Help)", url: url + "&walkthrough=1", api: noTeam() },
  {
    id: "participant-team.walkthrough-last",
    title: "Team: the walkthrough's last step",
    url: url + "&walkthrough=1",
    api: noTeam(),
    steps: [{ click: "text=Next" }, { click: "text=Next" }],
  },
  { id: "participant-team.admin-members", title: "Team: on a team, admin, Members", url: tab("members"), api: withTeam() },
  { id: "participant-team.member-menu", title: "Team: admin, a member's menu", url: tab("members"), api: withTeam(), steps: [{ click: '[aria-label="More for Ayu"]' }] },
  {
    id: "participant-team.remove-member",
    title: "Team: admin, remove a member",
    url: tab("members"),
    api: withTeam(),
    steps: [{ click: '[aria-label="More for Ayu"]' }, { click: "text=Remove from team" }],
  },
  {
    id: "participant-team.member",
    title: "Team: on a team, member",
    url: tab("members"),
    api: withTeam({ admin: false, members: members.map((m) => ({ ...m, admin: m.user_id === "u-nadia" })) }),
  },
  {
    id: "participant-team.just-joined",
    title: "Team: just joined",
    url: tab("members"),
    api: withTeam({ admin: false }),
    before: () => sessionStorage.setItem("sh-selfjoin-" + SLUG, "joined"),
  },
  { id: "participant-team.locked", title: "Team: locked", url: tab("members"), api: withTeam({ locked: true }) },
  {
    id: "participant-team.full",
    title: "Team: full",
    url: tab("members"),
    api: withTeam({ members: [...members, { user_id: "u-bayu", display_name: "Bayu" }] }),
  },
  { id: "participant-team.frozen", title: "Team: frozen (entries closed)", url: tab("members"), api: withTeam({ frozen: true }, {}, "closed") },
  { id: "participant-team.ended", title: "Team: event ended", url: tab("members"), api: withTeam({ frozen: true }, { [THREAD]: { messages: [] } }, "archived") },
  { id: "participant-team.message-dialog", title: "Team: message the organisers", url: tab("members"), api: withTeam(), steps: [{ click: "text=Message the organisers" }] },
  {
    id: "participant-team.message-empty",
    title: "Team: message the organisers with nothing written",
    url: tab("members"),
    api: withTeam(),
    steps: [{ click: "text=Message the organisers" }, { click: "text=Send" }],
  },
  { id: "participant-team.people", title: "Team: Find people", url: tab("people"), api: withTeam() },
  { id: "participant-team.people-empty", title: "Team: Find people, nobody looking", url: tab("people"), api: withTeam({}, { [OPEN]: { ...onTeamFinding, solo: [] } }) },
  { id: "participant-team.settings", title: "Team: Settings, admin", url: tab("settings"), api: withTeam() },
  { id: "participant-team.settings-dirty", title: "Team: Settings, a change not saved", url: tab("settings"), api: withTeam(), steps: [{ fill: ["#set-looking", "A designer and a pitcher"] }] },
  {
    id: "participant-team.settings-member",
    title: "Team: Settings, member",
    url: tab("settings"),
    api: withTeam({ admin: false, members: members.map((m) => ({ ...m, admin: m.user_id === "u-nadia" })) }),
  },
  { id: "participant-team.lock-dialog", title: "Team: lock with a request waiting", url: tab("settings"), api: withTeam(), steps: [{ click: "text=Lock team" }] },
  { id: "participant-team.new-link", title: "Team: make a new link", url: tab("settings"), api: withTeam(), steps: [{ click: "text=New link" }] },
  {
    id: "participant-team.leave-only-admin",
    title: "Team: the only admin leaving",
    url: tab("settings"),
    api: withTeam({ members: members.map((m) => ({ ...m, admin: !!m.you })), admin_successor: "Nadia" }),
    steps: [{ click: "text=Leave team" }],
  },
  {
    id: "participant-team.leave-last",
    title: "Team: the last member leaving",
    url: tab("settings"),
    api: withTeam({ members: [members[0]] }),
    steps: [{ click: "text=Delete team and leave" }],
  },
];

// The walkthrough opens by itself for someone with no team: every other entry has seen it.
const seen = () => localStorage.setItem("sh-participant-team-tour-done-u-siti", "1");
export const entries: Entry[] = list.map((e) =>
  e.id.includes("walkthrough") ? e : { ...e, before: () => (seen(), e.before?.()) },
);
