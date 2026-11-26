// The person's own level beside Your events: Account and Create event.
import type { ScreenDef } from "@/routes/registry";

export const screens: Record<string, ScreenDef> = {
  account: { kind: "home", title: "Account", load: () => import("./Account") },
  "organiser-create-event": { kind: "home", title: "Create event", load: () => import("./CreateEvent") },
};
