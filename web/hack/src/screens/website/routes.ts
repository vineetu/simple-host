// The participant's Website screen: /e/{slug}/team?s=website.
import type { ScreenDef } from "@/routes/registry";

export const screens: Record<string, ScreenDef> = {
  "participant-website": { kind: "event", title: "Website", load: () => import("./Website") },
};
