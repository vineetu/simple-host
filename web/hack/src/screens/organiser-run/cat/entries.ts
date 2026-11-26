// Entries (organiser) in every state. What teams hand in (the rules) is Settings → Entries.
import { refuse, type Entry } from "@kit/catalogue/harness";
import type { EntryItem, RunTeam } from "../data-entries";
import { E, org, orgApi, url } from "./fx";

export const teams: RunTeam[] = [
  { slug: "akar", name: "Akar", members: [{ display_name: "Rina" }, { display_name: "Bayu" }], site: { exists: true, url: "https://akar.green-campus.simple-hack.app/" } },
  { slug: "daun-hijau", name: "Daun Hijau", extended: true, deadline_override: "2026-11-23T10:00:00Z", deadline: "2026-11-23T10:00:00Z", members: [{ display_name: "Sari" }], site: { exists: false } },
  { slug: "sungai", name: "Sungai", members: [{ display_name: "Wawan" }, { display_name: "Tika" }, { display_name: "Joko" }], site: { exists: true, taken_down: true, url: "https://sungai.green-campus.simple-hack.app/" } },
  { slug: "panel-surya", name: "Panel Surya", members: [], site: { exists: false } },
];

export const rows: EntryItem[] = [
  { team: { slug: "akar", name: "Akar" }, status: "submitted", submitted_at: "2026-11-19T08:30:00Z", title: "Compost Map", tagline: "Find a compost drop near you.", site_exists: true },
  { team: { slug: "daun-hijau", name: "Daun Hijau" }, status: "draft", title: "Leaf Count", missing: ["video_url", "screenshot"], site_exists: false },
  { team: { slug: "sungai", name: "Sungai" }, status: "draft", title: "River Watch", problem: "Nobody knows when the river floods.", site_exists: true, site_taken_down: true },
  { team: { slug: "panel-surya", name: "Panel Surya" }, status: "draft", site_exists: false },
];

const counts = { submitted: 1, drafts: 2, not_started: 1, sites_live: 1 };
const usage = { saved_data_bytes: 1200000, history_bytes: 300000, screenshot_bytes: 250000, file_bytes: 32579 };

const lists = (o: Record<string, unknown> = {}) => ({
  ["GET " + E("entries")]: { entries: rows, counts },
  ["GET " + E("teams")]: { teams },
  ["GET " + E("usage")]: usage,
  ...o,
});

const late: EntryItem[] = [
  rows[0],
  { ...rows[1], status: "not_submitted", incomplete: true },
  { ...rows[2], status: "not_submitted", incomplete: true, excluded: true, missing: ["video_url"] },
  { ...rows[3], status: "not_submitted", incomplete: true },
];

const u = url("entries");
export const entries: Entry[] = [
  { id: "organiser-entries.loading", title: "Entries: loading", url: u, api: orgApi(lists({ ["GET " + E("entries")]: "pending" })) },
  { id: "organiser-entries.error", title: "Entries: the list did not load", url: u, api: orgApi(lists({ ["GET " + E("entries")]: refuse(500, "error", "Something went wrong. Try again.") })) },
  {
    id: "organiser-entries.before-any-entries",
    title: "Entries: before any entries (sign-up open)",
    url: u,
    api: orgApi(
      lists({ ["GET " + E("entries")]: { entries: [], counts: { not_started: 4 } }, ["GET " + E("usage")]: {} }),
      org({}, { stage: "open" }),
    ),
  },
  { id: "organiser-entries.loaded", title: "Entries: building, one team with more time", url: u, api: orgApi(lists()) },
  {
    id: "organiser-entries.deadline-passed",
    title: "Entries: deadline passed, incomplete entries to exclude or include",
    url: u,
    api: orgApi(lists({ ["GET " + E("entries")]: { entries: late, counts: { submitted: 1, not_submitted: 3, sites_live: 1 } } }), org({}, { stage: "closed", submission_deadline: "2026-11-19T10:00:00Z" })),
  },
  {
    id: "organiser-entries.excluded-toast",
    title: "Entries: an entry just excluded, with Undo",
    url: u,
    api: orgApi(lists({ ["GET " + E("entries")]: { entries: late, counts: { submitted: 1, not_submitted: 3, sites_live: 1 } } }), org({}, { stage: "closed", submission_deadline: "2026-11-19T10:00:00Z" })),
    steps: [{ click: "[aria-label='Exclude Daun Hijau']" }],
  },
  { id: "organiser-entries.give-more-time", title: "Entries: from an entry's Give more time (team picked)", url: url("entries", "&team=sungai"), api: orgApi(lists()) },
  { id: "organiser-entries.give-more-time-no-date", title: "Entries: Give more time with no date chosen", url: u, api: orgApi(lists()), steps: [{ click: "text=Give this team more time" }] },
  { id: "organiser-entries.clear-extension", title: "Entries: clear an extension, confirm", url: u, api: orgApi(lists()), steps: [{ click: "text=Clear this team’s extension" }] },
  { id: "organiser-entries.no-deadline", title: "Entries: no event deadline yet", url: u, api: orgApi(lists(), org({}, { submission_deadline: null })) },
  { id: "organiser-entries.entries-closed", title: "Entries: entries closed (judging)", url: u, api: orgApi(lists(), org({}, { stage: "judging" })) },
  { id: "organiser-entries.ended", title: "Entries: the event has ended", url: u, api: orgApi(lists(), org({}, { stage: "archived" })) },
  { id: "organiser-entries.admin-view", title: "Entries: a platform admin's read-only look", url: u, api: orgApi(lists(), org({ admin_view: true })) },
];
