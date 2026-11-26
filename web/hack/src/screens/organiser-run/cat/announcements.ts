// Announcements in every state (atlas organiser-announcements).
import { refuse, type Entry } from "@kit/catalogue/harness";
import type { Announcement } from "../data-announcements";
import { E, org, orgApi, url } from "./fx";

const u = url("announcements");
const list: Announcement[] = [
  { id: "a1", title: "Judging has started", body: "Judging has started. Entries, teams and websites can no longer be changed.", created_at: "2026-11-20T08:00:00Z", pinned: true, emails_sent: 21 },
  {
    id: "a2",
    title: "Lunch is in the courtyard",
    body: "Lunch is in the **courtyard** today, from noon. Vegetarian boxes are marked green. See the [map](https://green-campus.simple-hack.app/map) if you get lost.",
    created_at: "2026-11-19T04:30:00Z",
  },
  { id: "a3", title: "Welcome!", body: "Welcome to Green Campus Hack. Find a team on the Team tab.", created_at: "2026-11-18T01:00:00Z", emails_sent: 1 },
];
const A = { ["GET " + E("announcements")]: { announcements: list }, ["POST " + E("announcements/preview")]: { body_html: "<p>Judging has started. Entries, teams and websites can no longer be changed.</p>" } };
const noEmail = org({ organiser: { ...org().organiser, counts: { participants: 4, teams: 1, judges: 1, on_no_team: 0, emailable_participants: 0 } as Record<string, number> } });

export const entries: Entry[] = [
  { id: "organiser-announcements.loading", title: "Announcements: loading", url: u, api: orgApi({ ["GET " + E("announcements")]: "pending" }) },
  { id: "organiser-announcements.error", title: "Announcements: the list did not load", url: u, api: orgApi({ ["GET " + E("announcements")]: refuse(500, "error", "Something went wrong.") }) },
  { id: "organiser-announcements.empty", title: "Announcements: none yet", url: u, api: orgApi({ ["GET " + E("announcements")]: { announcements: [] } }) },
  { id: "organiser-announcements.loaded", title: "Announcements: posted list", url: u, api: orgApi(A) },
  { id: "organiser-announcements.email-ticked", title: "Announcements: email ticked, the button says how many", url: u, api: orgApi(A), steps: [{ click: "input[type=checkbox]" }] },
  { id: "organiser-announcements.required", title: "Announcements: Post with nothing typed", url: u, api: orgApi(A), steps: [{ click: "button[type=submit]" }] },
  { id: "organiser-announcements.title-typed", title: "Announcements: typing a title, the counter", url: u, api: orgApi(A), steps: [{ fill: ["input[maxlength='120']", "Pizza at six"] }] },
  { id: "organiser-announcements.add-link", title: "Announcements: Add link", url: u, api: orgApi(A), steps: [{ click: "text=Add link" }] },
  {
    id: "organiser-announcements.add-link-bad",
    title: "Announcements: Add link, not a web address",
    url: u,
    api: orgApi(A),
    steps: [{ click: "text=Add link" }, { fill: ["input[inputmode=url]", "javascript:alert(1)"] }, { click: "[role=dialog] button[type=submit]" }],
  },
  { id: "organiser-announcements.row-menu", title: "Announcements: a row's menu", url: u, api: orgApi(A), steps: [{ click: "[aria-label='More for Lunch is in the courtyard']" }] },
  { id: "organiser-announcements.edit", title: "Announcements: edit, with the preview", url: u, api: orgApi(A), steps: [{ click: "[aria-label='More for Judging has started']" }, { click: "text=Edit" }] },
  {
    id: "organiser-announcements.delete",
    title: "Announcements: delete asks first",
    url: u,
    api: orgApi(A),
    steps: [{ click: "[aria-label='More for Lunch is in the courtyard']" }, { click: "text=Delete" }],
  },
  { id: "organiser-announcements.no-one-to-email", title: "Announcements: no one to email", url: u, api: orgApi(A, noEmail) },
  { id: "organiser-announcements.ended", title: "Announcements: ended, read only", url: u, api: orgApi(A, org({}, { stage: "archived" })) },
  { id: "organiser-announcements.admin", title: "Announcements: platform admin, read only", url: u, api: orgApi(A, org({ admin_view: true })) },
];
