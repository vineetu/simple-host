// The service's answers the catalogue shows: a person in four events, an
// event as each role sees it, and an api() builder each family's entries
// start from (override any path; everything else answers {}).
import type { Route, Answer } from "@kit/catalogue/harness";
import type { EventDetail, EventRow, Need } from "@/lib/client";

/** The catalogue's clock (the visual checks freeze it here). */
export const NOW = new Date("2026-11-20T10:00:00Z");

export const me = { id: "u-siti", email: "siti@example.com", display_name: "Siti Rahma" };

export const events: EventRow[] = [
  { slug: "jakarta-ai-night", title: "Jakarta AI Night", stage: "judging", role: "participant", color: "#216c6a", approval_status: "approved", time_zone: "Asia/Jakarta" },
  { slug: "green-campus", title: "Green Campus Hack", stage: "building", role: "organiser", color: "#d9a520", time_zone: "Asia/Jakarta" },
  { slug: "health-sprint", title: "Health Sprint", stage: "open", role: "participant", color: "#8e5bb5", approval_status: "pending" },
  { slug: "fintech-jam", title: "Fintech Jam", stage: "judging", role: "judge", color: "#2a78d6" },
  { slug: "spring-2025", title: "Spring Hack 2025", stage: "archived", role: "participant", ended_at: "2025-04-12T10:00:00Z", color: "#b85c4f" },
];

export const todos: { events: { slug: string; title: string; role: EventRow["role"]; items: Need[] }[] } = {
  events: [
    { slug: "green-campus", title: "Green Campus Hack", role: "organiser", items: [{ kind: "application_new", text: "3 applications are waiting for you.", section: "people", count: 3 }] },
    { slug: "jakarta-ai-night", title: "Jakarta AI Night", role: "participant", items: [{ kind: "invite", text: "Team Lumbung invited you to join.", section: "team" }] },
  ],
};

/** detail is GET E for one of the events above, as its member sees it. */
export function detail(slug: string, o: Partial<EventDetail> & { me?: EventDetail["me"] } = {}): EventDetail {
  const row = events.find((e) => e.slug === slug)!;
  return {
    event: {
      slug,
      title: row.title,
      stage: row.stage,
      color: row.color,
      time_zone: row.time_zone || "UTC",
      judging_ends_at: "2026-11-22T12:00:00Z",
      results_published: false,
      announcement_times: ["2026-11-19T08:00:00Z"],
      scoring_open: true,
    },
    role: row.role,
    can: { take_part: row.role === "participant", judge: row.role === "judge", manage: row.role === "organiser" },
    me: { approval_status: row.approval_status, team: { slug: "lumbung", name: "Lumbung", admin: true, pending_requests: 0 } },
    needs_you: [{ id: "n1", kind: "news", text: "Judging has started.", dismissable: true }],
    ...o,
  };
}

/** api answers the person's account, events and to-dos, then each event's GET E; over wins for any path it names. */
export function api(over: Record<string, Answer> = {}): Route {
  return (method, path) => {
    const key = method + " " + path;
    if (key in over) return over[key];
    if (path in over) return over[path];
    if (path === "/v1/me") return me;
    if (path === "/v1/hack/events") return events;
    if (path === "/v1/hack/todos") return todos;
    const m = path.match(/^\/v1\/hack\/events\/([^/]+)$/);
    if (m && method === "GET" && events.some((e) => e.slug === m[1])) return detail(m[1]);
    return {};
  };
}
