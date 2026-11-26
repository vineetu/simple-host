// Event page in every state: each tab loaded, empty and read-only, each dialog and its checks.
import { refuse, type Entry, type Step } from "@kit/catalogue/harness";
import { adminView, E, ended, manage, orgApi } from "./fixtures";

const url = (tab: string, rest = "") => manage("event-page", "&tab=" + tab + rest);
// A 2 × 2 picture, as a logo or icon would come.
const PNG = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAIAAAACCAYAAABytg0kAAAAFklEQVR4nGNgYGD4z8DAwMDAwMAAAAAQAAG4a3lkAAAAAElFTkSuQmCC";

const content = {
  schedule: [
    { title: "Kickoff", description: "Welcome and rules, in the main hall.", start_at: "2026-11-28T02:00:00Z", end_at: "2026-11-28T03:00:00Z", prompt: "building" },
    { title: "Build time", description: "", start_at: "2026-11-28T03:00:00Z", end_at: "2026-11-29T06:00:00Z" },
    { title: "Demos", description: "Three minutes a team.", start_at: "2026-11-29T07:00:00Z", end_at: "2026-11-29T09:00:00Z" },
  ],
  fixed_schedule: [
    { key: "event_start", title: "Event starts", start_at: "2026-11-28T01:00:00Z", all_day: false, fixed: true, set_in: "basics" },
    { key: "entry_deadline", title: "Entries due", start_at: "2026-11-29T08:00:00Z", all_day: false, fixed: true, set_in: "entries", prompt: "closed" },
    { key: "event_end", title: "Event ends", start_at: "2026-11-29T10:00:00Z", all_day: false, fixed: true, set_in: "basics" },
  ],
  sponsors: [
    { name: "Bank Hijau", tier: "Gold", url: "https://bankhijau.example/", logo_data: PNG },
    { name: "Kopi Kampus", tier: "Community partner", url: "", logo_data: "" },
  ],
  faq: [
    { question: "Who can join?", answer: "Any student at the university, from any faculty." },
    { question: "Do I need a team?", answer: "No. Come alone and find a team on the day, or start one in the app." },
  ],
};
const emptyContent = { schedule: [], fixed_schedule: [], sponsors: [], faq: [] };
const teams = { teams: [{ slug: "a", track: { slug: "t1" } }, { slug: "b", track: { slug: "t1" } }, { slug: "c", track: { slug: "t2" } }] };
const tracks = [
  { slug: "t1", name: "Food waste", challenge: "Cut what the canteens throw away.", prize: "" },
  { slug: "t2", name: "Energy", challenge: "", prize: "Solar kit" },
];
const builtin = { mode: "builtin", has_custom_page: false, versions: [], upload_limits: "", site: "ev-green-campus", owner: "green-campus" };
const versions = [
  { version_number: 1, created_at: "2026-11-10T03:00:00Z" },
  { version_number: 2, created_at: "2026-11-15T09:30:00Z" },
];
const withUploads = { ...builtin, has_custom_page: true, versions };
const custom = { ...withUploads, mode: "custom", active_version: 2 };

const base = { [E + "/content"]: content, [E + "/website"]: builtin, [E + "/teams"]: teams };
const svc = (over: Record<string, unknown> = {}, ev: Record<string, unknown> = {}, o = {}) => orgApi({ ...base, ...over }, { tracks, ...ev }, o);

type Variant = [id: string, title: string, tab: string, api: ReturnType<typeof svc>, steps?: Step[], rest?: string];
const v: Variant[] = [
  ["loading", "Event page: loading", "basics", orgApi({ ["GET " + E]: "pending" })],
  ["error", "Event page: the event did not load", "basics", orgApi({ ["GET " + E]: refuse(500, "error", "Something went wrong. Try again.") })],
  // Basics
  ["basics", "Basics: name, tagline, dates and zone", "basics", svc()],
  ["basics-dirty", "Basics: unsaved changes", "basics", svc(), [{ fill: ["#f-tag", "Build the greenest campus tool."] }]],
  ["basics-no-title", "Basics: the title is empty", "basics", svc(), [{ fill: ["#f-title", ""] }, { click: "text=Save" }]],
  ["basics-refused", "Basics: the server refused the save", "basics", svc({ ["PATCH " + E]: refuse(400, "invalid_title", "The title must be one line, up to 120 characters.") }), [{ fill: ["#f-title", "Green Campus Hack 2026"] }, { click: "text=Save" }]],
  ["basics-leave-dirty", "Basics: switching tabs with unsaved changes", "basics", svc(), [{ fill: ["#f-tag", "Changed"] }, { click: "text=About" }]],
  ["basics-ended", "Basics: after the event ended, still editable", "basics", svc({}, ended)],
  ["basics-admin", "Basics: platform admin, read only", "basics", svc({}, {}, adminView)],
  ["basics-custom-site", "Basics: own website live", "basics", svc({ [E + "/website"]: custom })],
  // About
  ["about", "About: text, prizes, rules, code of conduct", "about", svc()],
  ["about-admin", "About: platform admin, read only", "about", svc({}, {}, adminView)],
  // Schedule
  ["schedule", "Schedule: typed and fixed rows by day, Now and Next", "schedule", svc()],
  ["schedule-empty", "Schedule: nothing scheduled yet", "schedule", svc({ [E + "/content"]: emptyContent })],
  ["schedule-add", "Schedule: Add time dialog", "schedule", svc(), [{ click: "text=Add time" }]],
  ["schedule-add-check", "Schedule: Add time, ends before it starts", "schedule", svc(), [
    { click: "text=Add time" },
    { fill: ["#d-title", "Lunch"] },
    { fill: ["#d-start", "2026-11-28"] },
    { fill: ["[aria-label='Starts time']", "12:00"] },
    { fill: ["#d-end", "2026-11-28"] },
    { fill: ["[aria-label='Ends time']", "11:00"] },
    { click: "button[type=submit]@0" },
  ]],
  ["schedule-edit", "Schedule: Edit time dialog", "schedule", svc(), [{ click: "[aria-label='More for Kickoff']" }, { click: "text=Edit" }]],
  ["schedule-ended", "Schedule: after the event ended, read only", "schedule", svc({}, ended)],
  ["schedule-failed", "Schedule: content did not load", "schedule", svc({ [E + "/content"]: refuse(500, "error", "Something went wrong. Try again.") })],
  // Tracks
  ["tracks", "Tracks: two tracks with team counts", "tracks", svc()],
  ["tracks-empty", "Tracks: no tracks yet", "tracks", svc({}, { tracks: [] })],
  ["tracks-add", "Tracks: Add track dialog, name missing", "tracks", svc(), [{ click: "text=Add track" }, { click: "button[type=submit]@0" }]],
  ["tracks-remove", "Tracks: remove asks first, naming the teams", "tracks", svc(), [{ click: "[aria-label='More for Food waste']" }, { click: "text=Remove" }]],
  ["tracks-ended", "Tracks: after the event ended, read only", "tracks", svc({}, ended)],
  // Sponsors
  ["sponsors", "Sponsors: in page order, with and without logo", "sponsors", svc()],
  ["sponsors-empty", "Sponsors: none yet", "sponsors", svc({ [E + "/content"]: emptyContent })],
  ["sponsors-menu", "Sponsors: a row's menu", "sponsors", svc(), [{ click: "[aria-label='More for Bank Hijau']" }]],
  ["sponsors-edit", "Sponsors: Edit sponsor dialog with logo", "sponsors", svc(), [{ click: "[aria-label='More for Bank Hijau']" }, { click: "text=Edit" }]],
  ["sponsors-add-check", "Sponsors: Add sponsor, bad link", "sponsors", svc(), [{ click: "text=Add sponsor" }, { fill: ["#d-name", "Acme"] }, { fill: ["#d-link", "acme.example"] }, { click: "button[type=submit]@0" }]],
  ["sponsors-admin", "Sponsors: platform admin, read only", "sponsors", svc({}, {}, adminView)],
  // FAQ
  ["faq", "FAQ: questions in order", "faq", svc()],
  ["faq-empty", "FAQ: no questions yet", "faq", svc({ [E + "/content"]: emptyContent })],
  ["faq-add-check", "FAQ: Add question, both missing", "faq", svc(), [{ click: "text=Add question" }, { click: "button[type=submit]@0" }]],
  ["faq-ended", "FAQ: after the event ended, read only", "faq", svc({}, ended)],
  // Look
  ["look", "Look: style, icon, colour and what shows", "look", svc()],
  ["look-icon", "Look: with an icon, a colour and a style", "look", svc({}, { icon_media_type: "image/png", icon_url: PNG, accent_color: "indigo", theme: "garden", gallery_open: false })],
  ["look-ended", "Look: after the event ended, icon locked", "look", svc({}, { ...ended, icon_media_type: "image/png", icon_url: PNG })],
  ["look-admin", "Look: platform admin, read only", "look", svc({}, {}, adminView)],
  // Website
  ["website", "Website: built-in page, no uploads yet", "website", svc()],
  ["website-uploads", "Website: built-in page, uploads ready", "website", svc({ [E + "/website"]: withUploads })],
  ["website-switch", "Website: switching to your own site asks first", "website", svc({ [E + "/website"]: withUploads }), [{ click: "[role=radio]@1" }]],
  ["website-custom", "Website: own website live", "website", svc({ [E + "/website"]: custom })],
  ["website-ended", "Website: after the event ended, read only", "website", svc({ [E + "/website"]: custom }, ended)],
  ["website-failed", "Website: settings did not load", "website", svc({ [E + "/website"]: refuse(500, "error", "Something went wrong. Try again.") })],
];

export const entries: Entry[] = [
  ...v.map(([id, title, tab, api, steps, rest]) => ({ id: "organiser-event-page." + id, title, url: url(tab, rest), api, steps })),
  // Old #hash links land on their tab.
  { id: "organiser-event-page.hash-appearance", title: "Event page: an old #appearance link opens Look", url: manage("event-page") + "#appearance", api: svc() },
];
