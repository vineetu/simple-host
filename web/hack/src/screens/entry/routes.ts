// The participant's Entry screen: /e/{slug}/team?s=entry.
import type { ScreenDef } from "@/routes/registry";

export const screens: Record<string, ScreenDef> = {
  "participant-entry": { kind: "event", title: "Entry", load: () => import("./Entry") },
};
