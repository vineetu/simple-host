// Entry detail (organiser) in every state.
import { refuse, type Entry } from "@kit/catalogue/harness";
import type { EntryItem, RunTeam } from "../data-entries";
import { rows, teams } from "./entries";
import { E, org, orgApi, url } from "./fx";

const full: EntryItem = {
  team: { slug: "akar", name: "Akar" },
  status: "submitted",
  submitted_at: "2026-11-19T08:30:00Z",
  title: "Compost Map",
  tagline: "Find a compost drop near you, and see what it takes.",
  problem: "Campus food waste goes to landfill because nobody knows where to compost.",
  audience: "Students and canteen staff",
  tools: ["Claude", "Leaflet"],
  maturity: "working",
  track: { name: "Waste" },
  video_url: "https://youtu.be/compost",
  code_url: "https://github.com/example/compost-map",
  has_screenshot: true,
  site_exists: true,
  description: "We mapped 14 drop points with the canteen staff.\nThe map updates when a bin is full.",
};

const lists = (team: RunTeam, entry: EntryItem | null, o: Record<string, unknown> = {}) => ({
  ["GET " + E("teams")]: { teams: [team, ...teams.filter((t) => t.slug !== team.slug)] },
  ["GET " + E("entries")]: { entries: entry ? [entry] : [], counts: {} },
  ["GET " + E("entry-rules")]: { fields: { code_url: "optional" } },
  ...o,
});

const akar = teams[0];
const at = (team: string) => url("entry", "&team=" + team);
const closed = org({}, { stage: "closed", submission_deadline: "2026-11-19T10:00:00Z" });

export const entries: Entry[] = [
  { id: "organiser-entry-detail.loading", title: "Entry: loading", url: at("akar"), api: orgApi(lists(akar, full, { ["GET " + E("teams")]: "pending" })) },
  { id: "organiser-entry-detail.error", title: "Entry: did not load", url: at("akar"), api: orgApi(lists(akar, full, { ["GET " + E("teams")]: refuse(500, "error", "Something went wrong. Try again.") })) },
  { id: "organiser-entry-detail.not-found", title: "Entry: no such team", url: at("gone"), api: orgApi(lists(akar, full)) },
  { id: "organiser-entry-detail.submitted", title: "Entry: submitted, site live", url: at("akar"), api: orgApi(lists(akar, full)) },
  {
    id: "organiser-entry-detail.submitted-rule-tightened",
    title: "Entry: submitted, then a rule asked for more (judging)",
    url: at("akar"),
    api: orgApi(lists(akar, { ...full, missing: ["screenshot"], has_screenshot: false, code_url: "" }, { ["GET " + E("entry-rules")]: { fields: { code_url: "off" } } }), org({}, { stage: "judging" })),
  },
  {
    id: "organiser-entry-detail.no-entry",
    title: "Entry: the team has not started one",
    url: at("panel-surya"),
    api: orgApi(lists(teams[3], null)),
  },
  {
    id: "organiser-entry-detail.draft-something-missing",
    title: "Entry: draft, something missing, with the team's extension",
    url: at("daun-hijau"),
    api: orgApi(lists(teams[1], { ...rows[1], tagline: "Count trees from a phone photo.", tools: ["Gemini"] })),
  },
  {
    id: "organiser-entry-detail.not-submitted-after-deadline",
    title: "Entry: not complete after the deadline",
    url: at("daun-hijau"),
    api: orgApi(lists(teams[1], { ...rows[1], status: "not_submitted", incomplete: true }), closed),
  },
  {
    id: "organiser-entry-detail.excluded",
    title: "Entry: excluded from judging",
    url: at("daun-hijau"),
    api: orgApi(lists(teams[1], { ...rows[1], status: "not_submitted", incomplete: true, excluded: true }), closed),
  },
  {
    id: "organiser-entry-detail.taken-down-by-you",
    title: "Entry: site taken down by you",
    url: at("akar"),
    api: orgApi(lists({ ...akar, site: { ...akar.site, taken_down: true, taken_down_reason: "The site shows a classmate's phone number." } }, { ...full, site_taken_down: true })),
  },
  {
    id: "organiser-entry-detail.taken-down-by-the-platform",
    title: "Entry: site taken down by Simple Host (Put back refused)",
    url: at("sungai"),
    api: orgApi(lists(teams[2], rows[2], { ["POST " + E("teams/sungai/restore")]: refuse(409, "platform_takedown", "Simple Host took this site down.") })),
    steps: [{ click: "text=Put back" }],
  },
  {
    id: "organiser-entry-detail.taken-down-toast",
    title: "Entry: site just taken down, with Undo",
    url: at("akar"),
    api: orgApi(lists(akar, full)),
    steps: [{ click: "text=Take this site down" }],
  },
  { id: "organiser-entry-detail.ended", title: "Entry: the event has ended", url: at("akar"), api: orgApi(lists(akar, full), org({}, { stage: "archived" })) },
  { id: "organiser-entry-detail.admin-view", title: "Entry: a platform admin's read-only look", url: at("akar"), api: orgApi(lists(akar, full), org({ admin_view: true })) },
];
