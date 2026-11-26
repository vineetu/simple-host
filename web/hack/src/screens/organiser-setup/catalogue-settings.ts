// Settings in every state: each of the eleven tabs, their empty states and dialogs, setup locked at judging, an ended event and an admin's inspection.
import { refuse, type Answer, type Entry, type Route } from "@kit/catalogue/harness";
import { E, manage, orgApi } from "./fixtures";

const url = (tab: string, rest = "") => manage("settings", "&tab=" + tab + rest);

const people = [
  { user_id: "u-siti", display_name: "Siti Rahma", email: "siti@example.com", role: "organiser", primary_organiser: true },
  { user_id: "u-rina", display_name: "Rina Hartono", email: "rina@example.com", role: "organiser" },
  { user_id: "u-budi", display_name: "Pak Budi", role: "judge" },
  { user_id: "u-ayu", display_name: "Ayu", role: "participant" },
];
const rubric = {
  criteria: [
    { id: "c1", name: "Impact", description: "Does it cut waste on campus?", weight: 50, max_points: 5 },
    { id: "c2", name: "Craft", description: "", weight: 30, max_points: 5 },
    { id: "c3", name: "Pitch", description: "", weight: 20, max_points: 5 },
  ],
};
const rules = {
  preset: "video_or_website",
  fields: { title: "required", problem: "required", tagline: "required", audience: "optional", track: "required", tools: "required", maturity: "required", description: "optional", video_url: "one_of", site: "one_of", code_url: "off", screenshot: "optional" },
  judge_first: "video",
  editable: true,
  has_tracks: true,
  summary: "a video link or a website",
};
const hooks = {
  notifications: [
    { id: "h1", url: "https://hooks.slack.com/services/T00/B00/XYZ", label: "Organisers’ Slack", events: ["record.added", "file.uploaded"], storage: "", state: "active", last_attempt_at: "2026-11-20T08:12:00Z", last_result: "ok" },
    { id: "h2", url: "https://example.org/hook", events: ["record.changed"], storage: "signups", state: "suspended", last_attempt_at: "2026-11-19T22:00:00Z", last_result: "timeout" },
  ],
  limits: { can_create: true, can_manage: true, history_days: 7 },
};
const voting = { enabled: true, opens_at: "2026-11-29T08:00:00Z", closes_at: "2026-11-29T10:00:00Z", eligibility: "all_signed_in", status: "upcoming", directory_listed: true };

const base: Record<string, Answer> = {
  ["GET " + E + "/registration"]: {
    questions: [
      { id: "q1", prompt: "Which faculty are you in?", required: true },
      { id: "q2", prompt: "Anything we should know?", required: false },
    ],
    approval_required: true,
    applications_open: true,
  },
  ["GET " + E + "/applications"]: [
    { user_id: "u-x", status: "pending", joined_via: "link" },
    { user_id: "u-y", status: "pending", joined_via: "link" },
    { user_id: "u-z", status: "pending", joined_via: "application" },
  ],
  ["GET " + E + "/voting"]: voting,
  ["GET " + E + "/entry-rules"]: rules,
  ["GET " + E + "/reminders"]: { offsets_minutes: [1440, 120], min_minutes: 30, max_minutes: 20160, max_count: 5, judging_ends_at: null },
  ["GET " + E + "/rubric"]: rubric,
  ["GET " + E + "/judging/settings"]: { assignment_mode: "automatic", judges_per_team: 2, score_mode: "normalised", tie_criterion_id: "c1", public_scores: false, public_ranks: true, organiser_judges: ["u-rina"] },
  ["GET " + E + "/people"]: people,
  ["GET " + E + "/results"]: { results: null, full_ranking: false },
  ["GET " + E + "/website/notifications"]: hooks,
  ["GET " + E + "/website/notifications/h1/deliveries"]: {
    deliveries: [
      { id: "d1", name: "record.added", state: "delivered", result: "ok", last_http: 200, created_at: "2026-11-20T08:12:00Z" },
      { id: "d2", name: "file.uploaded", state: "failed", result: "http_5xx", last_http: 502, created_at: "2026-11-20T07:40:00Z" },
    ],
  },
  ["GET " + E + "/leave"]: { case: "creator", slug: "green-campus", successors: [{ user_id: "u-rina", display_name: "Rina Hartono", email: "rina@example.com" }] },
};

/** svc is the service for Settings: the base answers, over on top, and the event's own fields. */
const svc = (over: Record<string, Answer> = {}, ev: Record<string, unknown> = {}, o: Record<string, unknown> = {}): Route => orgApi({ ...base, ...over }, ev, o);

/** withBody answers one call by what was sent (the dry run, a confirm), the rest from svc. */
const withBody =
  (r: Route, method: string, path: string, fn: (body: Record<string, unknown>) => Answer): Route =>
  (m, p, b) =>
    m === method && p === path ? fn((b || {}) as Record<string, unknown>) : r(m, p, b);

const custom = { website_mode: "custom" };

export const entries: Entry[] = [
  { id: "organiser-settings.loading", title: "Settings: a tab loading", url: url("signup"), api: svc({ ["GET " + E + "/registration"]: "pending" }) },
  { id: "organiser-settings.error", title: "Settings: a tab that did not load", url: url("judging"), api: svc({ ["GET " + E + "/rubric"]: refuse(500, "error", "Something went wrong. Try again.") }) },
  { id: "organiser-settings.signup", title: "Settings → Sign-up: questions, approval, applications, directory", url: url("signup"), api: svc() },
  {
    id: "organiser-settings.signup-empty",
    title: "Settings → Sign-up: no questions, applications off, not listed",
    url: url("signup"),
    api: svc({ ["GET " + E + "/registration"]: { questions: [], approval_required: false, applications_open: false }, ["GET " + E + "/voting"]: { directory_listed: false } }, { applications_open: false }),
  },
  {
    id: "organiser-settings.signup-load-failed",
    title: "Settings → Sign-up: the questions did not load, saving is off",
    url: url("signup"),
    api: svc({ ["GET " + E + "/registration"]: refuse(500, "error", "Something went wrong. Try again.") }),
  },
  {
    id: "organiser-settings.signup-waiting",
    title: "Settings → Sign-up: turning approval off with people waiting",
    url: url("signup"),
    api: svc(),
    steps: [{ click: "input[type=checkbox]" }],
  },
  { id: "organiser-settings.teams", title: "Settings → Teams", url: url("teams"), api: svc() },
  { id: "organiser-settings.entries", title: "Settings → Entries: deadline and what teams hand in", url: url("entries"), api: svc() },
  {
    id: "organiser-settings.entries-closed",
    title: "Settings → Entries: entries closed, rules can’t change",
    url: url("entries"),
    api: svc({ ["GET " + E + "/entry-rules"]: { ...rules, editable: false } }, { stage: "closed" }),
  },
  {
    id: "organiser-settings.entries-impact",
    title: "Settings → Entries: a rule change that affects entries already made",
    url: url("entries"),
    api: withBody(svc(), "PUT", E + "/entry-rules", (b) => (b.dry_run ? { impact: { submitted_missing: 2, drafts_newly_blocked: 3 } } : rules)),
    steps: [{ select: ["#rule-code_url", "required"] }, { click: "text=Save" }],
  },
  { id: "organiser-settings.reminders", title: "Settings → Reminders", url: url("reminders"), api: svc() },
  {
    id: "organiser-settings.reminders-empty",
    title: "Settings → Reminders: none set",
    url: url("reminders"),
    api: svc({ ["GET " + E + "/reminders"]: { offsets_minutes: [], min_minutes: 30, max_minutes: 20160, max_count: 5, judging_ends_at: "2026-11-30T10:00:00Z" } }),
  },
  {
    id: "organiser-settings.reminders-invalid",
    title: "Settings → Reminders: a reminder outside the limits",
    url: url("reminders"),
    api: svc(),
    steps: [{ fill: ["#rem-n", "20"] }, { click: "text=Add" }],
  },
  { id: "organiser-settings.judging", title: "Settings → Judging: rubric, tie-break, closes, assignment", url: url("judging"), api: svc() },
  { id: "organiser-settings.judging-locked", title: "Settings → Judging: locked while judging", url: url("judging"), api: svc({}, { stage: "judging", scoring_open: true }) },
  {
    id: "organiser-settings.judging-deletes-scores",
    title: "Settings → Judging: a rubric change that would delete scores",
    url: url("judging"),
    api: withBody(svc({}, { stage: "closed" }), "PUT", E + "/rubric", (b) =>
      b.confirm_score_deletion ? rubric : refuse(409, "score_deletion_confirmation", "Lowering Craft to 3 points deletes 14 scores above 3. This cannot be undone."),
    ),
    steps: [{ fill: ["#crit-1-p", "3"] }, { click: "text=Save rubric" }],
  },
  {
    id: "organiser-settings.judging-weights",
    title: "Settings → Judging: weights that don’t add up",
    url: url("judging"),
    api: svc({}, { stage: "closed" }),
    steps: [{ fill: ["#crit-0-w", "40"] }, { click: "text=Save rubric" }],
  },
  { id: "organiser-settings.results", title: "Settings → Results: ranking, visibility, people’s choice", url: url("results"), api: svc() },
  {
    id: "organiser-settings.results-remove-votes",
    title: "Settings → Results: narrowing who can vote removes votes",
    url: url("results"),
    api: withBody(svc(), "PUT", E + "/voting", (b) => (b.confirm_vote_removal ? { ...voting, votes_removed: 4 } : refuse(409, "vote_removal_confirmation", "Confirm removal of earlier votes.", { votes_to_remove: 4 }))),
    steps: [{ select: ["#res-vote-eligibility", "participants"] }, { click: "text=Save@2" }],
  },
  { id: "organiser-settings.details", title: "Settings → Organiser details", url: url("details"), api: svc() },
  {
    id: "organiser-settings.details-invalid",
    title: "Settings → Organiser details: a field that is wrong",
    url: url("details"),
    api: svc(),
    steps: [{ fill: ["#set-n", "0"] }, { click: "text=Save" }],
  },
  { id: "organiser-settings.organisers", title: "Settings → Organisers", url: url("organisers"), api: svc() },
  { id: "organiser-settings.organisers-empty", title: "Settings → Organisers: no co-organisers", url: url("organisers"), api: svc({ ["GET " + E + "/people"]: people.filter((p) => p.user_id !== "u-rina") }) },
  { id: "organiser-settings.organisers-remove", title: "Settings → Organisers: removing a co-organiser", url: url("organisers"), api: svc(), steps: [{ click: "text=Remove co-organiser" }] },
  {
    id: "organiser-settings.organisers-invited",
    title: "Settings → Organisers: an invitation just made",
    url: url("organisers"),
    api: svc({ ["POST " + E + "/organiser-invite"]: { url: "https://simple-hack.app/organiser/ORG-55k2", expires_at: "2026-11-27T10:00:00Z" } }),
    steps: [{ click: "text=Create invitation" }, { click: "text=Create invitation@1" }],
  },
  { id: "organiser-settings.links", title: "Settings → Links", url: url("links"), api: svc() },
  { id: "organiser-settings.links-new", title: "Settings → Links: making a new join link", url: url("links"), api: svc(), steps: [{ click: "text=New link" }] },
  { id: "organiser-settings.notifications-unavailable", title: "Settings → Notifications: the built-in page, so none yet", url: url("notifications"), api: svc() },
  { id: "organiser-settings.notifications", title: "Settings → Notifications: two webhooks", url: url("notifications"), api: svc({}, custom) },
  {
    id: "organiser-settings.notifications-empty",
    title: "Settings → Notifications: own website, none set up",
    url: url("notifications"),
    api: svc({ ["GET " + E + "/website/notifications"]: { notifications: [], limits: { can_create: true, can_manage: true } } }, custom),
  },
  { id: "organiser-settings.notifications-add", title: "Settings → Notifications: adding one", url: url("notifications"), api: svc({}, custom), steps: [{ click: "text=Add notification" }] },
  { id: "organiser-settings.notifications-deliveries", title: "Settings → Notifications: a webhook’s deliveries", url: url("notifications"), api: svc({}, custom), steps: [{ click: "text=Deliveries" }] },
  { id: "organiser-settings.danger", title: "Settings → Leave or delete", url: url("danger"), api: svc() },
  { id: "organiser-settings.danger-delete", title: "Settings → Leave or delete: delete asks for the name typed", url: url("danger"), api: svc(), steps: [{ click: "text=Delete event" }] },
  { id: "organiser-settings.ended", title: "Settings: an ended event, settings read only", url: url("judging"), api: svc({}, { stage: "archived", judging_ends_at: "2026-11-30T10:00:00Z" }) },
  { id: "organiser-settings.ended-danger", title: "Settings → Leave or delete: ended, delete still there", url: url("danger"), api: svc({}, { stage: "archived" }) },
  { id: "organiser-settings.admin", title: "Settings: a platform admin’s read-only inspection", url: url("signup"), api: svc({}, {}, { admin_view: true }) },
  { id: "organiser-settings.old-anchor", title: "Settings: an old #tie-break link opens Judging", url: manage("settings") + "#tie-break", api: svc() },
];
