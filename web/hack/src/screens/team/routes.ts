// The participant's Team screen: /e/{slug}/team?s=team (with team-admin functions).
import type { ScreenDef } from "@/routes/registry";

export const screens: Record<string, ScreenDef> = {
  "participant-team": { kind: "event", title: "Team", load: () => import("./Team") },
};
