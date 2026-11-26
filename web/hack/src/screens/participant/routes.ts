// The participant's area: /e/{slug}/team?s=… (Team, Entry and Website join this family when rebuilt).
import type { ScreenDef } from "@/routes/registry";

export const screens: Record<string, ScreenDef> = {
  "participant-home": { kind: "event", title: "Overview", load: () => import("./Home") },
  "participant-schedule": { kind: "event", title: "Schedule", load: () => import("./Schedule") },
  "participant-announcements": { kind: "event", title: "Announcements", load: () => import("./Announcements") },
  "participant-results": { kind: "event", title: "Results", load: () => import("./Results") },
  "participant-submissions": { kind: "event", title: "Submissions", load: () => import("./Submissions") },
};
