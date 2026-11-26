// The organiser's running-the-event screens: /e/{slug}/manage?s=overview,
// people, teams, team, entries, entry, announcements, results. (The setup
// screens, Event page, Settings, Judging and Assignments, are their own family.)
import type { ScreenDef } from "@/routes/registry";

export const screens: Record<string, ScreenDef> = {
  "organiser-overview": { kind: "event", title: "Home", load: () => import("./Overview") },
  "organiser-people": { kind: "event", title: "People", load: () => import("./People") },
  "organiser-teams": { kind: "event", title: "Teams", tabTitle: (q) => (q.get("tab") === "solo" ? "Not on a team" : "Teams") + " · Teams", load: () => import("./Teams") },
  "organiser-team-detail": { kind: "event", title: "Team", load: () => import("./TeamDetail") },
  "organiser-entries": { kind: "event", title: "Entries", load: () => import("./Entries") },
  "organiser-entry-detail": { kind: "event", title: "Entry", load: () => import("./EntryDetail") },
  "organiser-announcements": { kind: "event", title: "Announcements", load: () => import("./Announcements") },
  "organiser-results": { kind: "event", title: "Results", load: () => import("./Results") },
};
