// Home in every state (atlas organiser-overview): each next step the server
// can choose, the confirms, Change stage, End, Publish with ties, "Tell
// participants?", and the read-only views. (The Ask panel and the
// walkthrough have their own files.)
import { refuse, type Answer, type Entry } from "@kit/catalogue/harness";
import type { Checklist, Step } from "../data-overview";
import { E, org, orgApi, url, type Org } from "./fx";

const u = url("overview");
const off = { ["GET " + E("assistant")]: { enabled: false }, "/v1/hack/preferences": { organiser_walkthrough_done: true } };
const settings = { assignment_mode: "automatic", judges_per_team: 2, judges: [{ name: "Rina Wijaya" }, { name: "Tomo Hadi" }, { name: "Siti Rahma", organiser: true }] };
const dashboard = {
  coverage: { target: 2, min: 1, max: 3, fix_href: "/e/green-campus/manage?s=assignments", short: [{ name: "Ojek AI", have: 1 }] },
  entries: { judged: 5, no_entry: 1, excluded: 0 },
};
const teams = { teams: [{ slug: "lumbung", name: "Lumbung", track: { name: "Food" } }, { slug: "ojek-ai", name: "Ojek AI", track: null }, { slug: "kopi", name: "Kopi Club", track: null }] };
const standings = {
  results: [
    { team_id: "t1", team_name: "Lumbung", rank: 1, total: 8.6, tied: false },
    { team_id: "t2", team_name: "Kopi Club", rank: 2, total: 7.9, tied: true },
    { team_id: "t3", team_name: "Ojek AI", rank: 2, total: 7.9, tied: true },
    { team_id: "t4", team_name: "Sawah Data", rank: null, total: null, tied: false },
  ],
};

const checklist = (stage: string, next: Step | null): Checklist => ({
  role: "organiser",
  stage,
  next,
  items: [
    { id: "o.basics", label: "Add event basics", state: "done", group: "earlier_required" },
    { id: "o.rubric", label: "Set the rubric", state: "todo", group: "earlier_required", actionable: true, target_id: "o.rubric", link: { href: "/e/green-campus/manage?s=settings&tab=judging#rubric" } },
    { id: "o.teams", label: "Teams are forming", state: "done", group: "current", detail: "6 teams" },
    { id: "o.no-team", label: "Place people with no team", state: "todo", group: "current", actionable: true, target_id: "o.teams", link: { href: "/e/green-campus/manage?s=teams" }, detail: "2 people", can_dismiss: true },
    { id: "o.announce", label: "Post an update", state: "not_needed", group: "current", optional: true, can_dismiss: true, actionable: true, link: { href: "/e/green-campus/manage?s=announcements" } },
    { id: "o.faq", label: "Add common questions", state: "todo", group: "earlier_optional", optional: true, actionable: true, target_id: "o.faq", link: { href: "/e/green-campus/manage?s=event-page&tab=faq" } },
  ],
});

/** at is Home at a stage with a next step (and the checklist off unless given). */
const at = (stage: string, step: Step | null, o: Partial<Org> = {}, ev: Record<string, unknown> = {}) => org({ next_step: step, checklist: null, ...o }, { stage, ...ev });
const home = (ev: Org, over: Record<string, Answer> = {}) => orgApi({ ...off, [E("judging/settings")]: settings, ...over }, ev);

const building = at("building", { kind: "stage", stage: "closed", label: "Close entries" }, { checklist: checklist("building", { kind: "stage", stage: "closed", label: "Close entries" }) });
const closed = at("closed", { kind: "stage", stage: "judging", label: "Start judging" });
const results = at("results", { kind: "stage", stage: "archived", label: "End event" }, {}, { results_published: false });
const published = at("results", { kind: "stage", stage: "archived", label: "End event" }, {}, { results_published: true, results_published_at: "2026-11-19T11:00:00Z" });
const nobody = { participants: 0, teams: 0, judges: 0, on_no_team: 0 } as Record<string, number>;
const confirmNext = { click: "[data-tour='next-move']" };

export const entries: Entry[] = [
  { id: "organiser-overview.loading", title: "Home: loading", url: u, api: orgApi({ ...off, [E("judging/settings")]: "pending" }, building) },
  { id: "organiser-overview.frame-error", title: "Home: the event did not load", url: u, api: orgApi({ ...off, ["GET " + E()]: refuse(500, "error", "Something went wrong. Try again.") }) },
  {
    id: "organiser-overview.entry-rules",
    title: "Home: choose what teams hand in (a new event)",
    url: u,
    api: home(at("draft", { kind: "entry_rules", label: "Choose what teams hand in" })),
  },
  { id: "organiser-overview.not-open-yet", title: "Home: not open yet", url: u, api: home(at("draft", { kind: "stage", stage: "open", label: "Open sign-up" }, { organiser: { join_url: "https://simple-hack.app/join/GRN-4821", judge_url: "https://simple-hack.app/judge/JDG-7713", counts: nobody } })) },
  { id: "organiser-overview.building", title: "Home: building, with this stage's checklist", url: u, api: home(building) },
  { id: "organiser-overview.checklist-not-needed", title: "Home: a checklist row marked not needed (Undo)", url: u, api: home(building), steps: [{ click: "text=Not needed" }] },
  {
    id: "organiser-overview.deadline-passed",
    title: "Home: the entry deadline passed, still building",
    url: u,
    api: home(at("building", { kind: "stage", stage: "closed", label: "Close entries", deadline_passed: true }, {}, { submission_deadline: "2026-11-19T10:00:00Z" })),
  },
  { id: "organiser-overview.confirming", title: "Home: confirming the move (people not on a team)", url: u, api: home(building), steps: [confirmNext] },
  {
    id: "organiser-overview.move-failed",
    title: "Home: the move failed",
    url: u,
    api: home(building, { ["POST " + E("stage")]: refuse(409, "stage_changed", "Someone else moved the event to Entries closed. Reload to see it.") }),
    steps: [confirmNext, { click: "text=Close entries@1" }],
  },
  {
    id: "organiser-overview.tell-participants",
    title: "Home: offering to tell participants",
    url: u,
    api: home(building, { ["POST " + E("stage")]: { ok: true } }),
    steps: [confirmNext, { click: "text=Close entries@1" }],
  },
  { id: "organiser-overview.closed", title: "Home: closed, ready to start judging", url: u, api: home(closed) },
  {
    id: "organiser-overview.start-judging",
    title: "Home: confirming Start judging (pool and setup)",
    url: u,
    api: home(closed, { [E("teams")]: teams, [E("judging/dashboard")]: dashboard }),
    steps: [confirmNext],
  },
  {
    id: "organiser-overview.panel-no-track",
    title: "Home: panel mode, teams with no track",
    url: u,
    api: home(closed, { [E("judging/settings")]: { ...settings, assignment_mode: "panel" }, [E("teams")]: teams, [E("judging/dashboard")]: dashboard }),
  },
  { id: "organiser-overview.judging", title: "Home: judging", url: u, api: home(at("judging", { kind: "stage", stage: "results", label: "Finish judging" }, { can: { manage: true, judge: true, take_part: false } })) },
  { id: "organiser-overview.results-not-published", title: "Home: results, not published", url: u, api: home(results) },
  {
    id: "organiser-overview.publish-ties",
    title: "Home: publish, with a tie to place",
    url: u,
    api: home(results, { [E("results")]: {}, [E("judging/preview")]: standings }),
    steps: [confirmNext],
  },
  {
    id: "organiser-overview.results-published",
    title: "Home: results published, End is next",
    url: u,
    api: home(published, { [E("results")]: standings, [E("judging/preview")]: standings }),
  },
  {
    id: "organiser-overview.scores-changed",
    title: "Home: published scores changed, Publish again",
    url: u,
    api: home(published, { [E("results")]: standings, [E("judging/preview")]: { results: standings.results.map((r) => (r.team_id === "t4" ? { ...r, rank: 4, total: 6.1 } : r)) } }),
  },
  { id: "organiser-overview.end-confirm", title: "Home: End event asks first", url: u, api: home(published, { [E("results")]: standings, [E("judging/preview")]: standings }), steps: [confirmNext] },
  {
    id: "organiser-overview.end-not-possible",
    title: "Home: End not possible, nobody joined",
    url: u,
    api: home(at("results", { kind: "stage", stage: "archived", label: "End event", blocked_reason: "archive_needs_participants" }, { organiser: { counts: nobody } }, { results_published: true })),
  },
  { id: "organiser-overview.ended", title: "Home: ended", url: u, api: home(at("archived", { kind: "none" }, {}, { results_published: true })) },
  {
    id: "organiser-overview.schedule-cue",
    title: "Home: a schedule row lines up with the next move",
    url: u,
    api: home(at("open", { kind: "stage", stage: "building", label: "Start building", cue: { key: "row", title: "Kickoff", start_at: "2026-11-20T09:30:00Z", now: true } })),
  },
  {
    id: "organiser-overview.applications-waiting",
    title: "Home: applications waiting",
    url: u,
    api: home(at("open", { kind: "stage", stage: "building", label: "Start building" }, { needs_you: [{ id: "n1", kind: "application_new", text: "3 applications are waiting for you.", section: "people", count: 3 }] }), {
      [E("applications")]: [{ status: "pending" }, { status: "pending" }, { status: "pending" }],
    }),
  },
  { id: "organiser-overview.change-stage", title: "Home: Change stage", url: u, api: home(building), steps: [{ click: "[data-tour='change-stage']" }] },
  {
    id: "organiser-overview.change-stage-back",
    title: "Home: Change stage, moving back",
    url: u,
    api: home(building),
    steps: [{ click: "[data-tour='change-stage']" }, { select: ["#stage-pick", "open"] }],
  },
  {
    id: "organiser-overview.change-stage-judging",
    title: "Home: Change stage to Judging, with the setup summary",
    url: u,
    api: home(building, { [E("judging/dashboard")]: dashboard }),
    steps: [{ click: "[data-tour='change-stage']" }, { select: ["#stage-pick", "judging"] }],
  },
  { id: "organiser-overview.admin-view", title: "Home: viewing as platform admin", url: u, api: home(at("building", { kind: "none" }, { admin_view: true })) },
];
