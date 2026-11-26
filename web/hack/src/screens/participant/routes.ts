// The participant's area: /e/{slug}/team?s=… (the other sections join this family when rebuilt).
import type { ScreenDef } from "@/routes/registry";

export const screens: Record<string, ScreenDef> = {
  "participant-submissions": { kind: "event", title: "Submissions", load: () => import("./Submissions") },
};
