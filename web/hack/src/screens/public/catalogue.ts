// The link pages, sign-in, Find a hackathon and People's choice in every state.
// Link pages: join, apply, judge, co-organiser and team links.
// Ids are the UX atlas's screen.state ids.
import { refuse, type Entry } from "@kit/catalogue/harness";
import { api, detail } from "@/catalogue/fixtures";
import { entries as signin } from "./signin.catalogue";
import { entries as vote } from "./vote.catalogue";

const signedOut = () => localStorage.removeItem("apiKey");
const offline = refuse(0, "offline", "Cannot reach the server.");
const noName = { "/v1/me": { id: "u-new", email: "new@example.com", login: "new@example.com" } };
const agree = { click: "text=I agree to the code of conduct" };

// Join link
const JOIN = "/v1/hack/join/kopi-2026";
const joinInfo = {
  slug: "kopi-fest",
  title: "Kopi Fest Hack",
  organiser_name: "Dewi Lestari",
  organisation: "Kopi Labs",
  joinable: true,
  stage: "open",
  signup_questions: [
    { id: "q1", prompt: "What do you want to build?", required: true },
    { id: "q2", prompt: "Anything we should know?", required: false },
  ],
  coc_default: "Be kind, be safe, and build things that help people. Harassment of any kind is not allowed.",
  coc_text: "Photos are taken on the day; tell us if you'd rather not be in them.",
  account_name: "Siti Rahma",
};
const join = (o: Record<string, unknown> = {}, more: Record<string, unknown> = {}) => api({ [JOIN]: { ...joinInfo, ...o }, ...more });

// Apply
const APPLY = "/v1/hack/events/kopi-fest/apply";
const applyInfo = {
  title: "Kopi Fest Hack",
  organiser_name: "Dewi Lestari",
  organisation: "Kopi Labs",
  starts_at: "2026-12-05T00:00:00+07:00",
  ends_at: "2026-12-06T00:00:00+07:00",
  time_zone: "Asia/Jakarta",
  applications_open: true,
  me: null,
  signup_questions: joinInfo.signup_questions,
  coc_default: joinInfo.coc_default,
  coc_text: joinInfo.coc_text,
  account_name: "Siti Rahma",
};
const apply = (o: Record<string, unknown> = {}, more: Record<string, unknown> = {}) => api({ [APPLY]: { ...applyInfo, ...o }, ...more });
// Signed out the server leaves out "me" and everything the organisers wrote for applicants.
const applyOut = () => api({ [APPLY]: { title: applyInfo.title, organiser_name: applyInfo.organiser_name, organisation: applyInfo.organisation, starts_at: applyInfo.starts_at, ends_at: applyInfo.ends_at, time_zone: applyInfo.time_zone, applications_open: true, coc_default: applyInfo.coc_default } });

// Judge invite
const JUDGE = "/v1/hack/judge/judge-kopi";
const judgeInfo = {
  slug: "kopi-fest",
  title: "Kopi Fest Hack",
  organiser_name: "Dewi Lestari",
  organisation: "Kopi Labs",
  tagline: "Two days of building for coffee farmers.",
  starts_at: "2026-12-05T09:00:00+07:00",
  ends_at: "2026-12-06T17:00:00+07:00",
  time_zone: "Asia/Jakarta",
  joinable: true,
  stage: "building",
  coc_default: joinInfo.coc_default,
  coc_text: "",
  account_name: "Siti Rahma",
};
const judge = (o: Record<string, unknown> = {}, more: Record<string, unknown> = {}) => api({ [JUDGE]: { ...judgeInfo, ...o }, ...more });

// Co-organiser invite
const ORG = "/v1/hack/organiser/org-kopi";
const orgInfo = { title: "Kopi Fest Hack", joinable: true, organiser_name: "Dewi Lestari", coc_default: joinInfo.coc_default, coc_text: "", account_name: "Siti Rahma" };
const org = (o: Record<string, unknown> = {}, more: Record<string, unknown> = {}) => api({ [ORG]: { ...orgInfo, ...o }, ...more });

// Team link
const TEAM = "/v1/hack/team-links/kopi-kode";
const teamInfo = {
  event: { slug: "jakarta-ai-night", title: "Jakarta AI Night", applications_open: true },
  team: { name: "Kopi Kode", member_count: 3, team_size_max: 5, track: { name: "Food" } },
  you: { status: "ok" },
};
const team = (o: Record<string, unknown> = {}, more: Record<string, unknown> = {}) => api({ [TEAM]: { ...teamInfo, ...o }, ...more });

export const entries: Entry[] = [
  // Join
  { id: "public-join.loading", title: "Join link: loading", url: "/join/kopi-2026", api: api({ [JOIN]: "pending" }) },
  { id: "public-join.signed-out", title: "Join link: signed out, what joining asks", url: "/join/kopi-2026", api: join(), before: signedOut },
  {
    id: "public-join.signed-out-no-questions",
    title: "Join link: signed out, no questions",
    url: "/join/kopi-2026",
    api: join({ signup_questions: [], coc_text: "" }),
    before: signedOut,
  },
  { id: "public-join.form", title: "Join link: signed in, the form", url: "/join/kopi-2026", api: join() },
  { id: "public-join.form-no-name", title: "Join link: the account has no name yet", url: "/join/kopi-2026", api: join({ account_name: "" }, noName) },
  { id: "public-join.form-approval", title: "Join link: the organisers approve each person", url: "/join/kopi-2026", api: join({ approval_required: true, signup_questions: [] }) },
  { id: "public-join.form-errors", title: "Join link: sent with nothing filled in", url: "/join/kopi-2026", api: join({ account_name: "" }, noName), steps: [{ click: "text=Join event" }] },
  {
    id: "public-join.too-many-tries",
    title: "Join link: too many tries",
    url: "/join/kopi-2026",
    api: join({ signup_questions: [] }, { ["POST " + JOIN]: refuse(429, "rate_limited", "rate limited") }),
    steps: [agree, { click: "text=Join event" }],
  },
  { id: "public-join.already-in", title: "Join link: already in the event", url: "/join/jakarta", api: api({ "/v1/hack/join/jakarta": { ...joinInfo, slug: "jakarta-ai-night", title: "Jakarta AI Night" } }) },
  { id: "public-join.pending", title: "Join link: application waiting", url: "/join/health", api: api({ "/v1/hack/join/health": { ...joinInfo, slug: "health-sprint", title: "Health Sprint" } }) },
  {
    id: "public-join.rejected",
    title: "Join link: application not accepted",
    url: "/join/health",
    api: api({
      "/v1/hack/join/health": { ...joinInfo, slug: "health-sprint", title: "Health Sprint" },
      "GET /v1/hack/events/health-sprint": detail("health-sprint", { me: { approval_status: "rejected", team: null } }),
    }),
  },
  { id: "public-join.closed", title: "Join link: sign-up has closed", url: "/join/kopi-2026", api: join({ joinable: false, stage: "building" }) },
  { id: "public-join.not-open", title: "Join link: the event is not open yet", url: "/join/kopi-2026", api: join({ joinable: false, stage: "draft" }), before: signedOut },
  { id: "public-join.over", title: "Join link: the event is over", url: "/join/kopi-2026", api: join({ joinable: false, stage: "archived" }) },
  { id: "public-join.invalid", title: "Join link: the link does not work", url: "/join/old", api: api({ "/v1/hack/join/old": refuse(404, "invalid_code", "invalid code") }), before: signedOut },
  { id: "public-join.offline", title: "Join link: the server cannot be reached", url: "/join/kopi-2026", api: api({ [JOIN]: offline }) },

  // Apply
  { id: "public-apply.loading", title: "Apply: loading", url: "/e/kopi-fest/apply", api: api({ [APPLY]: "pending" }) },
  { id: "public-apply.signed-out", title: "Apply: signed out", url: "/e/kopi-fest/apply", api: applyOut(), before: signedOut },
  { id: "public-apply.form", title: "Apply: the form", url: "/e/kopi-fest/apply", api: apply() },
  { id: "public-apply.form-no-name", title: "Apply: the account has no name yet", url: "/e/kopi-fest/apply", api: apply({ account_name: "" }, noName) },
  { id: "public-apply.under-18", title: "Apply: under 18 chosen, the guardian note", url: "/e/kopi-fest/apply", api: apply({ signup_questions: [] }), steps: [{ click: "[aria-label='Under 18']" }] },
  { id: "public-apply.form-errors", title: "Apply: sent with nothing filled in", url: "/e/kopi-fest/apply", api: apply({ account_name: "" }, noName), steps: [{ click: "text=Send application" }] },
  {
    id: "public-apply.server-field-error",
    title: "Apply: the server refuses the country",
    url: "/e/kopi-fest/apply",
    api: apply({ signup_questions: [] }, { ["POST " + APPLY]: refuse(400, "invalid_country", "Choose a country from the list.") }),
    steps: [agree, { click: "text=Send application" }],
  },
  {
    id: "public-apply.too-many-tries",
    title: "Apply: too many tries",
    url: "/e/kopi-fest/apply",
    api: apply({ signup_questions: [] }, { ["POST " + APPLY]: refuse(429, "rate_limited", "rate limited") }),
    steps: [agree, { click: "text=Send application" }],
  },
  { id: "public-apply.sent", title: "Apply: sent, waiting for the organisers", url: "/e/kopi-fest/apply", api: apply({ me: { role: "participant", approval_status: "pending" } }) },
  { id: "public-apply.accepted", title: "Apply: accepted, already in", url: "/e/kopi-fest/apply", api: apply({ me: { role: "participant", approval_status: "approved" } }) },
  { id: "public-apply.not-accepted", title: "Apply: not accepted", url: "/e/kopi-fest/apply", api: apply({ me: { role: "participant", approval_status: "rejected" } }) },
  { id: "public-apply.other-role", title: "Apply: already a judge here", url: "/e/kopi-fest/apply", api: apply({ me: { role: "judge" } }) },
  {
    id: "public-apply.applications-off",
    title: "Apply: the event takes no applications",
    url: "/e/kopi-fest/apply",
    api: apply({ applications_open: false, closed_reason: "applications_off" }),
    before: signedOut,
  },
  { id: "public-apply.sign-up-over", title: "Apply: sign-up has closed", url: "/e/kopi-fest/apply", api: apply({ applications_open: false, closed_reason: "stage" }) },
  { id: "public-apply.not-found", title: "Apply: not open yet, or no such event", url: "/e/nope/apply", api: api({ "/v1/hack/events/nope/apply": refuse(404, "event_not_found", "not found") }), before: signedOut },
  { id: "public-apply.offline", title: "Apply: the server cannot be reached", url: "/e/kopi-fest/apply", api: api({ [APPLY]: offline }) },

  // Judge invite
  { id: "public-judge.loading", title: "Judge invite: loading", url: "/judge/judge-kopi", api: api({ [JUDGE]: "pending" }) },
  { id: "public-judge.signed-out", title: "Judge invite: signed out", url: "/judge/judge-kopi", api: judge(), before: signedOut },
  { id: "public-judge.form", title: "Judge invite: accept", url: "/judge/judge-kopi", api: judge() },
  { id: "public-judge.form-no-name", title: "Judge invite: the account has no name yet", url: "/judge/judge-kopi", api: judge({ account_name: "" }, noName) },
  { id: "public-judge.form-errors", title: "Judge invite: sent without agreeing", url: "/judge/judge-kopi", api: judge(), steps: [{ click: "text=Join as judge" }] },
  {
    id: "public-judge.just-joined-already",
    title: "Judge invite: accepting finds them already in",
    url: "/judge/judge-kopi",
    api: judge({}, { ["POST " + JUDGE]: refuse(409, "already_member", "already a member") }),
    steps: [agree, { click: "text=Join as judge" }],
  },
  { id: "public-judge.already-in", title: "Judge invite: already in the event", url: "/judge/fintech", api: api({ "/v1/hack/judge/fintech": { ...judgeInfo, slug: "fintech-jam", title: "Fintech Jam" } }) },
  { id: "public-judge.invite-no-longer-valid", title: "Judge invite: no longer open", url: "/judge/judge-kopi", api: judge({ joinable: false, stage: "results" }) },
  { id: "public-judge.not-open", title: "Judge invite: the event is not open yet", url: "/judge/judge-kopi", api: judge({ joinable: false, stage: "draft" }), before: signedOut },
  { id: "public-judge.bad-link", title: "Judge invite: the link does not work", url: "/judge/old", api: api({ "/v1/hack/judge/old": refuse(404, "invalid_code", "invalid code") }), before: signedOut },
  { id: "public-judge.offline", title: "Judge invite: the server cannot be reached", url: "/judge/judge-kopi", api: api({ [JUDGE]: offline }) },

  // Co-organiser invite
  { id: "public-organiser-invite.loading", title: "Co-organiser invite: loading", url: "/organiser/org-kopi", api: api({ [ORG]: "pending" }) },
  { id: "public-organiser-invite.signed-out", title: "Co-organiser invite: signed out", url: "/organiser/org-kopi", api: org(), before: signedOut },
  { id: "public-organiser-invite.form", title: "Co-organiser invite: accept", url: "/organiser/org-kopi", api: org() },
  { id: "public-organiser-invite.form-no-name", title: "Co-organiser invite: the account has no name yet", url: "/organiser/org-kopi", api: org({ account_name: "" }, noName) },
  { id: "public-organiser-invite.form-errors", title: "Co-organiser invite: sent without agreeing", url: "/organiser/org-kopi", api: org({ account_name: "" }, noName), steps: [{ click: "text=Accept and co-organise" }] },
  {
    id: "public-organiser-invite.already-member",
    title: "Co-organiser invite: already in the event",
    url: "/organiser/org-kopi",
    api: org({}, { ["POST " + ORG]: refuse(409, "already_member", "already a member") }),
    steps: [agree, { click: "text=Accept and co-organise" }],
  },
  { id: "public-organiser-invite.invite-no-longer-valid", title: "Co-organiser invite: no longer open", url: "/organiser/old", api: api({ "/v1/hack/organiser/old": refuse(410, "gone", "gone") }) },
  { id: "public-organiser-invite.closed", title: "Co-organiser invite: the event can't take organisers", url: "/organiser/org-kopi", api: org({ joinable: false }) },
  { id: "public-organiser-invite.offline", title: "Co-organiser invite: the server cannot be reached", url: "/organiser/org-kopi", api: api({ [ORG]: offline }) },

  // Team link
  { id: "public-team-link.loading", title: "Team link: loading", url: "/team/kopi-kode", api: api({ [TEAM]: "pending" }) },
  { id: "public-team-link.signed-out", title: "Team link: signed out", url: "/team/kopi-kode", api: team({ you: null }), before: signedOut },
  { id: "public-team-link.ready", title: "Team link: ready to join", url: "/team/kopi-kode", api: team() },
  {
    id: "public-team-link.join-refused",
    title: "Team link: joining is refused",
    url: "/team/kopi-kode",
    api: team({}, { "POST /v1/hack/events/jakarta-ai-night/teams/join": refuse(409, "team_full", "full") }),
    steps: [{ click: "text=Join Kopi Kode" }],
  },
  { id: "public-team-link.already-on-this-team", title: "Team link: already on this team", url: "/team/kopi-kode", api: team({ you: { status: "on_this_team" } }) },
  { id: "public-team-link.on-another-team", title: "Team link: on another team", url: "/team/kopi-kode", api: team({ you: { status: "on_other_team", team_name: "SheBuilds" } }) },
  { id: "public-team-link.locked", title: "Team link: the team is locked", url: "/team/kopi-kode", api: team({ reason: "locked" }) },
  { id: "public-team-link.full", title: "Team link: the team is full", url: "/team/kopi-kode", api: team({ reason: "full", team: { name: "Kopi Kode", member_count: 5, team_size_max: 5 } }) },
  { id: "public-team-link.not-in-the-event", title: "Team link: not in the event, it takes applications", url: "/team/kopi-kode", api: team({ you: { status: "not_in_event" } }) },
  {
    id: "public-team-link.not-in-the-event-closed",
    title: "Team link: not in the event, no applications",
    url: "/team/kopi-kode",
    api: team({ you: { status: "not_in_event" }, event: { ...teamInfo.event, applications_open: false } }),
  },
  { id: "public-team-link.waiting-for-approval", title: "Team link: waiting for approval", url: "/team/kopi-kode", api: team({ you: { status: "pending" } }) },
  { id: "public-team-link.rejected", title: "Team link: not accepted into the event", url: "/team/kopi-kode", api: team({ you: { status: "rejected" } }) },
  { id: "public-team-link.judge-or-organiser", title: "Team link: a judge or organiser", url: "/team/kopi-kode", api: team({ you: { status: "judge" } }) },
  { id: "public-team-link.teams-closed", title: "Team link: teams can't change now", url: "/team/kopi-kode", api: team({ reason: "teams_closed" }) },
  { id: "public-team-link.link-doesnt-work", title: "Team link: the link does not work", url: "/team/old", api: api({ "/v1/hack/team-links/old": refuse(404, "team_link_not_found", "not found") }), before: signedOut },
  { id: "public-team-link.offline", title: "Team link: the server cannot be reached", url: "/team/kopi-kode", api: api({ [TEAM]: offline }) },
  ...signin,
  ...vote,
];
