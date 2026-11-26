// People in every state (atlas organiser-people).
import { refuse, type Entry } from "@kit/catalogue/harness";
import type { Application, Person } from "../data-people";
import { E, org, orgApi, url } from "./fx";

const u = url("people");
const apps: Application[] = [
  {
    user_id: "u-ayu",
    display_name: "Ayu Lestari",
    email: "ayu@example.com",
    status: "pending",
    joined_via: "applied",
    joined_at: "2026-11-17T03:20:00Z",
    city: "Bandung",
    country: "ID",
    age_range: "18_24",
    gender: "woman",
    answers: [
      { prompt: "What do you want to build?", answer: "A ledger for rice co-ops, kept by voice message." },
      { prompt: "Have you been to a hackathon before?", answer: "" },
    ],
  },
  { user_id: "u-raf", display_name: "Rafi Hakim", email: "rafi@example.com", status: "pending", joined_via: "link", joined_at: "2026-11-18T09:05:00Z", age_range: "under_18", country: "MY" },
  { user_id: "u-dew", display_name: "Dewi Sari", status: "pending", joined_via: "applied", joined_at: "2026-11-19T12:40:00Z" },
];
const people: Person[] = [
  { user_id: "u-siti", display_name: "Siti Rahma", role: "organiser", primary_organiser: true, joined_at: "2026-11-01T02:00:00Z" },
  { user_id: "u-bud", display_name: "Budi Santoso", role: "organiser", joined_at: "2026-11-02T02:00:00Z" },
  { user_id: "u-nad", display_name: "Nadia Putri", role: "participant", approval_status: "approved", team: { slug: "lumbung", name: "Lumbung" }, joined_at: "2026-11-10T02:00:00Z" },
  { user_id: "u-ayu", display_name: "Ayu Lestari", role: "participant", approval_status: "pending", joined_at: "2026-11-17T03:20:00Z" },
  { user_id: "u-tom", display_name: "Tomas Wu", role: "participant", approval_status: "rejected", joined_at: "2026-11-12T02:00:00Z" },
  { user_id: "u-hen", display_name: "Hendra Wijaya", role: "judge", joined_at: "2026-11-15T02:00:00Z" },
];
const lists = (a: Application[] = apps, p: Person[] = people) => ({ ["GET " + E("applications")]: a, ["GET " + E("people")]: p });
const none = lists([], people.filter((p) => p.approval_status !== "pending"));

export const entries: Entry[] = [
  { id: "organiser-people.loading", title: "People: loading", url: u, api: orgApi({ ["GET " + E("applications")]: "pending" }) },
  { id: "organiser-people.error", title: "People: did not load", url: u, api: orgApi({ ...lists(), ["GET " + E("people")]: refuse(500, "error", "Something went wrong. Try again.") }) },
  { id: "organiser-people.loaded", title: "People: three applications waiting, then everyone", url: u, api: orgApi(lists()) },
  { id: "organiser-people.no-pending", title: "People: nobody waiting (also how applications off, nothing waiting looks)", url: u, api: orgApi(none) },
  { id: "organiser-people.draft", title: "People: sign-up not open yet", url: u, api: orgApi({ ["GET " + E("applications")]: [], ["GET " + E("people")]: people.slice(0, 2) }, org({}, { stage: "draft" })) },
  {
    id: "organiser-people.filters-hide-everything",
    title: "People: filters hide everything waiting",
    url: u,
    api: orgApi(lists()),
    steps: [{ select: ["#app-filter-from", "link"] }, { select: ["#app-filter-age", "35_plus"] }],
  },
  { id: "organiser-people.judges", title: "People: Everyone filtered to judges (?role=judge)", url: u + "&role=judge", api: orgApi(none) },
  { id: "organiser-people.reject", title: "People: reject asks first", url: u, api: orgApi(lists()), steps: [{ click: "text=Reject" }] },
  { id: "organiser-people.row-menu", title: "People: a person's menu", url: u, api: orgApi(none), steps: [{ click: "[aria-label='More for Nadia Putri']" }] },
  {
    id: "organiser-people.remove",
    title: "People: remove a participant asks first",
    url: u,
    api: orgApi(none),
    steps: [{ click: "[aria-label='More for Nadia Putri']" }, { click: "text=Remove from event" }],
  },
  { id: "organiser-people.ended", title: "People: ended, nothing changes", url: u, api: orgApi(lists(), org({}, { stage: "archived" })) },
  { id: "organiser-people.admin", title: "People: platform admin, read only", url: u, api: orgApi(lists(), org({ admin_view: true })) },
];
