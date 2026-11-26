// The person's home level: Your events (account and create-event join this family when rebuilt).
import type { ScreenDef } from "@/routes/registry";

export const screens: Record<string, ScreenDef> = {
  "landing-your-events": { kind: "home", title: "Your events", load: () => import("./YourEvents") },
};
