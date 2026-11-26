// The organiser's setup screens: /e/{slug}/manage?s=event-page|settings|judging|assignments
// (the run screens, Overview to Results, are their own family).
import type { ScreenDef } from "@/routes/registry";
import { TAB_LABEL, TABS, type Tab } from "./eventpage-data";

export const screens: Record<string, ScreenDef> = {
  "organiser-event-page": {
    kind: "event",
    title: "Event page",
    // "Look · Event page · Real Night", as before.
    tabTitle: (q, ev) => {
      const t = q.get("tab") as Tab;
      return (TABS.includes(t) ? TAB_LABEL[t] : TAB_LABEL.basics) + " · Event page · " + ev.title;
    },
    load: () => import("./EventPage"),
  },
  "organiser-settings": { kind: "event", title: "Settings", load: () => import("./Settings") },
  "organiser-judging": { kind: "event", title: "Judging", load: () => import("./Judging") },
  "organiser-judging-assignments": { kind: "event", title: "Assignment grid", load: () => import("./Assignments") },
};
