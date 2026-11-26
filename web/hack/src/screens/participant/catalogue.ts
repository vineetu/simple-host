// The participant's reading screens in every state (Overview, Schedule,
// Announcements, Results, Submissions), the walkthrough, and the event
// frame's own states shown on them.
import { refuse, type Answer, type Entry } from "@kit/catalogue/harness";
import { api, detail } from "@/catalogue/fixtures";
import type { EventDetail, Submission } from "@/lib/client";
import type { Progress } from "./data";

const url = "/e/jakarta-ai-night/team?s=submissions";
const SUBS = "/v1/hack/events/jakarta-ai-night/submissions";
const subs: Submission[] = [
  {
    team: "lumbung",
    team_name: "Lumbung",
    team_members: "Siti, Ayu, Nadia",
    title: "Rice Ledger",
    tagline: "A shared ledger farmers keep by voice message.",
    problem: "Village co-ops lose track of who lent what.",
    audience: "Rice co-ops in West Java",
    tools: ["Claude", "Google Sheets"],
    maturity: "working",
    track: { name: "Food" },
    site_url: "https://lumbung.jakarta-ai-night.simple-hack.app/",
    video_url: "https://youtu.be/abc",
    has_screenshot: true,
  },
  {
    team: "ojek-ai",
    team_name: "Ojek AI",
    title: "Ride Buddy",
    tagline: "",
    problem: "Drivers waste hours waiting between rides.",
    audience: "",
    tools: [],
    maturity: "concept",
    code_url: "https://github.com/example/ride-buddy",
    has_screenshot: false,
  },
  { team: "kopi", team_name: "Kopi Club", title: "", tools: ["Figma"], maturity: "partial", site_url: "javascript:alert(1)", has_screenshot: true },
];

const SLUG = "jakarta-ai-night";
const E = "/v1/hack/events/" + SLUG;
const HOME = `/e/${SLUG}/team?s=home`;
const SEEN = () => localStorage.setItem("sh-participant-tour-done-u-siti", "1");

/** ev is Jakarta AI Night while teams build, as Siti sees it; o changes it. */
function ev(o: { event?: Record<string, unknown>; me?: Record<string, unknown>; progress?: Progress | null; checklist?: unknown; needs_you?: EventDetail["needs_you"] } = {}): EventDetail {
  const base = detail(SLUG);
  return {
    ...base,
    needs_you: [],
    ...o,
    event: { ...base.event, stage: "building", submission_deadline: "2026-11-22T10:00:00Z", url: "https://jakarta-ai-night.simple-hack.app/", ...(o.event || {}) },
    me: { ...base.me, team: { slug: "lumbung", name: "Lumbung", admin: false, pending_requests: 0 }, ...(o.me || {}) },
  } as EventDetail;
}

const steps = (summary: Record<string, unknown>, evidence: Record<string, unknown>, submit: Record<string, unknown>) => [
  { id: "team", done: true, detail: "Lumbung, with Ayu and Nadia." },
  { id: "summary", filled: 4, of: 6, ...summary },
  { id: "evidence", summary: "a video or your website", ...evidence },
  { id: "submit", status: "draft", ...submit },
];
const prog = (o: Partial<Progress> & { s?: Record<string, unknown>; e?: Record<string, unknown>; u?: Record<string, unknown> } = {}): Progress => {
  const { s = {}, e = {}, u = {}, ...rest } = o;
  return {
    steps: steps({ done: false, missing: ["tools", "maturity"], ...s }, { done: false, ...e }, u) as Progress["steps"],
    next: "summary",
    title: "Rice Ledger",
    deadline: "2026-11-22T10:00:00Z",
    server_now: "2026-11-20T10:00:00Z",
    ...rest,
  };
};
const ready = (o: Partial<Progress> = {}) => prog({ next: "submit", s: { done: true, filled: 6, missing: [] }, e: { done: true }, ...o });

const news = {
  announcements: [
    { id: "a1", title: "Demos move to the main hall", body_html: "<p>Bring your laptop charger. Demos start at <strong>4 pm</strong> sharp.</p>", created_at: "2026-11-20T08:00:00Z", pinned: true },
    { id: "a2", title: "Lunch is here", body_html: "<p>Nasi goreng in the kitchen.</p>", created_at: "2026-11-19T05:00:00Z" },
    { id: "a3", title: "Welcome", body_html: "<p>Teams can form from now.</p>", created_at: "2026-11-18T02:00:00Z" },
  ],
};
const leaveOnTeam = { case: "participant", slug: SLUG, rejoin: "join", team: { name: "Lumbung", others: ["Ayu", "Nadia"], others_count: 2 } };

/** home is Overview with the event as o says, and the rest answered as an ordinary day. */
function home(id: string, title: string, d: EventDetail, more: Record<string, Answer> = {}, extra: Partial<Entry> = {}): Entry {
  return {
    id: "participant-home." + id,
    title: "Overview: " + title,
    url: HOME,
    api: api({ ["GET " + E]: d, [E + "/announcements"]: news, [E + "/leave"]: d.me?.team ? leaveOnTeam : { case: "participant", slug: SLUG, rejoin: "join", team: null }, [E + "/content"]: { schedule: [], fixed_schedule: [] }, [E + "/teams/open"]: { enabled: true, teams: [{}, {}], pending: [] }, ...more }),
    before: SEEN,
    ...extra,
  };
}
const noTeam = { team: null };

const schedule = {
  schedule: [
    { title: "Opening talk", description: "In the main hall.", start_at: "2026-11-20T02:00:00Z", end_at: "2026-11-20T03:00:00Z" },
    { title: "Build time", start_at: "2026-11-20T03:00:00Z", end_at: "2026-11-20T11:00:00Z" },
    { title: "Mentor hour", description: "Book a slot at the desk.", start_at: "2026-11-20T10:10:00Z", end_at: "2026-11-20T11:10:00Z" },
    { title: "Demos", start_at: "2026-11-21T09:00:00Z", end_at: "2026-11-21T11:00:00Z" },
  ],
  fixed_schedule: [
    { key: "event_start", title: "Event starts", start_at: "2026-11-19T17:00:00Z", all_day: true },
    { key: "entry_deadline", title: "Entries due", start_at: "2026-11-22T10:00:00Z" },
  ],
};

const MINE = E + "/my-results";
const RES = E + "/results";
const ranking = [
  { rank: 1, team_name: "Ojek AI", raw_total: 88.5, deciding_mode: "raw" },
  { rank: 2, team_name: "Lumbung", raw_total: 81.25, deciding_mode: "raw" },
  { rank: 3, team_name: "Kopi Club", raw_total: 70, deciding_mode: "raw" },
  { rank: null, team_name: "Late Team", deciding_mode: "raw" },
];
const myEntry = {
  title: "Rice Ledger",
  tagline: "A shared ledger farmers keep by voice message.",
  problem: "Village co-ops lose track of who lent what.",
  audience: "Rice co-ops in West Java",
  tools: ["Claude", "Google Sheets"],
  maturity: "working",
  track: { name: "Food" },
  site_url: "https://lumbung.jakarta-ai-night.simple-hack.app/",
  video_url: "https://youtu.be/abc",
  video_host: "YouTube",
};
const mine = { published: true, rank: 2, team_name: "Lumbung", raw_total: 81.25, deciding_mode: "raw", ranked_teams: 3, comments: ["Clear problem, lovely voice flow.", "The ledger needs an undo."], shown_publicly: true, entry: myEntry };
const results = (m: Answer, r: Answer = { ranking }) => api({ ["GET " + E]: ev({ event: { stage: "results", results_published: true } }), [MINE]: m, [RES]: r });
const RURL = `/e/${SLUG}/team?s=results`;
const SURL = `/e/${SLUG}/team?s=schedule`;
const AURL = `/e/${SLUG}/team?s=announcements`;

export const entries: Entry[] = [
  // Overview
  home("loading", "loading", ev(), { [E + "/announcements"]: "pending" }),
  home("no-team", "no team yet, the whole path shown", ev({ event: { stage: "open" }, me: noTeam })),
  home("invite", "no team, an invite waiting", ev({ me: noTeam }), { [E + "/teams/open"]: { enabled: true, teams: [], pending: [{ kind: "invite", team_name: "Kopi Club" }] } }),
  home("summary-incomplete", "on a team, summary incomplete", ev({ progress: prog() })),
  home("evidence", "summary done, work not shown yet", ev({ progress: prog({ next: "evidence", s: { done: true, filled: 6, missing: [] } }), checklist: { items: [{ id: "p.evidence", state: "todo", target_id: "p.website", link: { href: `/e/${SLUG}/team?s=website` } }] } })),
  home("ready", "ready, not submitted", ev({ progress: ready() })),
  home("submit-confirm", "Submit asks first", ev({ progress: ready() }), {}, { steps: [{ click: "text=Submit" }] }),
  home("submitted", "submitted, can still change it", ev({ progress: prog({ next: "", s: { done: true, filled: 6, missing: [] }, e: { done: true }, u: { done: true, status: "submitted", submitted_at: "2026-11-20T08:12:00Z" } }) })),
  home("under-an-hour", "under an hour left, not submitted", ev({ event: { submission_deadline: "2026-11-20T10:40:00Z" }, progress: prog({ deadline: "2026-11-20T10:40:00Z" }) })),
  home("deadline-passed", "deadline passed, not complete", ev({ event: { submission_deadline: "2026-11-20T09:00:00Z" }, me: { team: { slug: "lumbung", name: "Lumbung", frozen: true } }, progress: prog({ deadline: "2026-11-20T09:00:00Z", u: { status: "not_submitted" } }) })),
  home("extension", "the team has more time", ev({ event: { submission_deadline: "2026-11-20T09:00:00Z" }, me: { team: { slug: "lumbung", name: "Lumbung", deadline: "2026-11-20T15:00:00Z" } }, progress: prog({ deadline: "2026-11-20T15:00:00Z", deadline_is_extension: true, event_deadline: "2026-11-20T09:00:00Z" }) })),
  home("just-placed", "just placed on a team", ev({ progress: prog() }), {}, { before: () => (SEEN(), localStorage.setItem("sh-team-baseline-u-siti-" + SLUG, "")) }),
  home("removed", "taken off the team", ev({ me: { team: null, team_removal_notice: "You were taken off your team. Find or start another one." } })),
  home("all-set", "entries closed, site published", ev({ event: { stage: "closed" }, me: { team: { slug: "lumbung", name: "Lumbung", frozen: true, site: { exists: true, url: "https://lumbung.jakarta-ai-night.simple-hack.app/" } } } })),
  home("judging", "judges are scoring", ev({ event: { stage: "judging" } })),
  home("results", "results published", ev({ event: { stage: "results", results_published: true } })),
  home("ended", "event ended, leaving closed", ev({ event: { stage: "archived" } }), { [E + "/leave"]: { case: "participant", blocked: { code: "event_closed", message: "This event has ended." } } }),
  home("join-requests", "team admin, people asked to join", ev({ me: { team: { slug: "lumbung", name: "Lumbung", admin: true, pending_requests: 2 } }, progress: prog() })),
  home("schedule-soon", "a schedule row starts soon", ev({ progress: prog() }), { [E + "/content"]: schedule }),
  home(
    "needs-you",
    "something waits on you",
    ev({
      progress: prog(),
      needs_you: [
        { kind: "join_request", text: "2 people asked to join Lumbung.", section: "team", count: 2 },
        { id: "n1", kind: "news", text: "You're an admin of Lumbung now. You can say yes to join requests and lock the team.", section: "team", dismissable: true },
      ],
    }),
  ),
  home("leave-team", "leave, on a team with others", ev({ progress: prog() }), {}, { steps: [{ click: "text=Leave event" }] }),
  home("leave-only-member", "leave, the only member", ev({ progress: prog() }), { [E + "/leave"]: { case: "participant", slug: SLUG, rejoin: "join", team: { name: "Lumbung", deleted_on_leave: true, others: [], others_count: 0 } } }, { steps: [{ click: "text=Leave event" }] }),
  home("leave-no-team", "leave, no team", ev({ me: noTeam }), { [E + "/leave"]: { case: "participant", slug: SLUG, rejoin: "join", team: null } }, { steps: [{ click: "text=Leave event" }] }),
  home("leave-frozen", "leave, after the deadline", ev({ event: { stage: "judging" } }), { [E + "/leave"]: { case: "participant", slug: SLUG, team: { name: "Lumbung", frozen: true } } }),
  { ...home("walkthrough", "the walkthrough opens itself once", ev({ progress: prog() })), before: undefined },
  { ...home("walkthrough-step", "the walkthrough, Your entry step", ev({ progress: prog() }), {}, { steps: [{ click: "text=Next" }, { click: "text=Next" }, { click: "text=Next" }] }), before: undefined },
  {
    id: "participant-home.frame-rejected",
    title: "Event frame: application not accepted",
    url: `/e/health-sprint/team?s=home`,
    api: api({ "GET /v1/hack/events/health-sprint": detail("health-sprint", { me: { approval_status: "rejected", team: null } }) }),
  },

  // Schedule
  { id: "participant-schedule.loading", title: "Schedule: loading", url: SURL, api: api({ [E + "/content"]: "pending" }) },
  { id: "participant-schedule.error", title: "Schedule: did not load", url: SURL, api: api({ [E + "/content"]: refuse(500, "error", "Something went wrong. Try again.") }) },
  { id: "participant-schedule.empty", title: "Schedule: nothing yet", url: SURL, api: api({ [E + "/content"]: { schedule: [], fixed_schedule: [] } }) },
  { id: "participant-schedule.loaded", title: "Schedule: on now, next, and a row starting soon", url: SURL, api: api({ [E + "/content"]: schedule }) },
  {
    id: "participant-schedule.extension",
    title: "Schedule: the team's own deadline",
    url: SURL,
    api: api({ [E + "/content"]: { schedule: [], fixed_schedule: [{ key: "team_deadline", title: "Entries due for your team", start_at: "2026-11-22T16:00:00Z" }] } }),
  },

  // Announcements
  { id: "participant-announcements.loading", title: "Announcements: loading", url: AURL, api: api({ [E + "/announcements"]: "pending" }) },
  { id: "participant-announcements.error", title: "Announcements: did not load", url: AURL, api: api({ [E + "/announcements"]: refuse(500, "error", "Something went wrong. Try again.") }) },
  { id: "participant-announcements.empty", title: "Announcements: none yet", url: AURL, api: api({ [E + "/announcements"]: { announcements: [] } }) },
  { id: "participant-announcements.loaded", title: "Announcements: pinned first, the unread badge cleared", url: AURL, api: api({ [E + "/announcements"]: news }) },

  // Results
  { id: "participant-results.loading", title: "Results: loading", url: RURL, api: results("pending") },
  { id: "participant-results.error", title: "Results: did not load", url: RURL, api: results(refuse(500, "error", "Something went wrong. Try again.")) },
  { id: "participant-results.no-team", title: "Results: not on a team", url: RURL, api: results(refuse(409, "no_team", "You are not on a team.")) },
  { id: "participant-results.not-published", title: "Results: not published yet", url: RURL, api: results({ published: false }) },
  { id: "participant-results.loaded", title: "Results: place, comments, public card and standings", url: RURL, api: results(mine) },
  { id: "participant-results.winners-only", title: "Results: winners only, not a winner", url: RURL, api: results({ ...mine, shown_publicly: false }, { winners: ranking.slice(0, 1) }) },
  {
    id: "participant-results.track-winner",
    title: "Results: winners only, a track winner",
    url: RURL,
    api: results({ ...mine, track_winner: true, track: { name: "Food", prize: "A week at the co-op" }, comments: [] }, { winners: ranking.slice(0, 2) }),
  },
  { id: "participant-results.not-public", title: "Results: not scored, entry not public", url: RURL, api: results({ published: true, rank: null, team_name: "Lumbung", shown_publicly: false }, { ranking }) },

  // Submissions
  { id: "participant-submissions.loading", title: "Submissions: loading", url, api: api({ [SUBS]: "pending" }) },
  { id: "participant-submissions.not-open", title: "Submissions: before judging", url, api: api({ [SUBS]: refuse(409, "submissions_not_open", "Submissions are not open yet.") }) },
  { id: "participant-submissions.error", title: "Submissions: did not load", url, api: api({ [SUBS]: refuse(500, "error", "Something went wrong. Try again.") }) },
  { id: "participant-submissions.empty", title: "Submissions: none yet", url, api: api({ [SUBS]: { submissions: [] } }) },
  { id: "participant-submissions.loaded", title: "Submissions: three teams, one yours", url, api: api({ [SUBS]: { submissions: subs } }) },
  // The event frame, shown on this screen.
  { id: "participant-submissions.frame-loading", title: "Event frame: the event is loading", url, api: api({ "GET /v1/hack/events/jakarta-ai-night": "pending" }) },
  {
    id: "participant-submissions.frame-pending",
    title: "Event frame: waiting for approval",
    url: "/e/health-sprint/team?s=submissions",
    api: api({ "GET /v1/hack/events/health-sprint": detail("health-sprint", { me: { approval_status: "pending", team: null } }) }),
  },
  {
    id: "participant-submissions.frame-not-in-event",
    title: "Event frame: not in this event",
    url: "/e/old-one/team?s=submissions",
    api: api({ "GET /v1/hack/events/old-one": refuse(404, "event_not_found", "Event not found."), "GET /v1/hack/events/old-one/public": { title: "Old One" } }),
  },
  {
    id: "participant-submissions.frame-not-found",
    title: "Event frame: no such event",
    url: "/e/nope/team?s=submissions",
    api: api({ "GET /v1/hack/events/nope": refuse(404, "event_not_found", "Event not found."), "GET /v1/hack/events/nope/public": refuse(404, "event_not_found", "Event not found.") }),
  },
];
