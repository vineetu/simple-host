// Your events in every state. Ids are the UX atlas's screen.state ids.
import { refuse, type Entry } from "@kit/catalogue/harness";
import { api, events } from "@/catalogue/fixtures";

const leavePreview = (o: Record<string, unknown>) => api({ "GET /v1/hack/events/jakarta-ai-night/leave": { case: "participant", slug: "jakarta-ai-night", rejoin: "join", ...o } });

export const entries: Entry[] = [
  { id: "landing-your-events.loading", title: "Your events: loading", url: "/events", api: api({ "/v1/hack/events": "pending" }) },
  { id: "landing-your-events.empty", title: "Your events: not in any event yet", url: "/events", api: api({ "/v1/hack/events": [], "/v1/hack/todos": { events: [] } }) },
  { id: "landing-your-events.error", title: "Your events: the list did not load", url: "/events", api: api({ "/v1/hack/events": refuse(500, "error", "Something went wrong. Try again.") }) },
  { id: "landing-your-events.loaded", title: "Your events: current, past and what needs you", url: "/events", api: api() },
  {
    id: "landing-your-events.only-past",
    title: "Your events: nothing on now, past shown open",
    url: "/events?past=1",
    api: api({ "/v1/hack/events": events.filter((e) => e.stage === "archived"), "/v1/hack/todos": { events: [] } }),
  },
  { id: "landing-your-events.row-menu", title: "Your events: a row's menu", url: "/events", api: api(), steps: [{ click: "[aria-label='More for Jakarta AI Night']" }] },
  {
    id: "landing-your-events.leave",
    title: "Your events: leave, a participant on a team",
    url: "/events",
    api: leavePreview({ team: { name: "Lumbung", others: ["Ayu", "Nadia"], others_count: 2 } }),
    steps: [{ click: "[aria-label='More for Jakarta AI Night']" }, { click: "text=Leave this event…" }],
  },
  {
    id: "landing-your-events.leave-last-organiser",
    title: "Your events: leave, the only organiser chooses end or delete",
    url: "/events",
    api: api({ "GET /v1/hack/events/green-campus/leave": { case: "last_organiser", slug: "green-campus", close_options: { end: { blocked: false } } } }),
    steps: [{ click: "[aria-label='More for Green Campus Hack']" }, { click: "text=Leave this event…" }, { click: "[aria-label='Delete the event']" }],
  },
  {
    id: "landing-your-events.left",
    title: "Your events: after leaving, the toast",
    url: "/events",
    api: api(),
    before: () => sessionStorage.setItem("hack-flash", "You left Spring Hack 2025."),
  },
];
