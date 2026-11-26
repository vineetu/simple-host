// Submissions in every state, plus the event frame's own states shown on it.
import { refuse, type Entry } from "@kit/catalogue/harness";
import { api, detail } from "@/catalogue/fixtures";
import type { Submission } from "@/lib/client";

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

export const entries: Entry[] = [
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
