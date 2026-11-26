// The organiser's setup screens: /e/{slug}/manage?s=event-page|settings|judging|assignments
// (the run screens, Overview to Results, are their own family).
import type { ScreenDef } from "@/routes/registry";

export const screens: Record<string, ScreenDef> = {
  "organiser-event-page": { kind: "event", title: "Event page", load: () => import("./EventPage") },
  "organiser-settings": { kind: "event", title: "Settings", load: () => import("./Settings") },
  "organiser-judging": { kind: "event", title: "Judging", load: () => import("./Judging") },
  "organiser-judging-assignments": { kind: "event", title: "Assignment grid", load: () => import("./Assignments") },
};
