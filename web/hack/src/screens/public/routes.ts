// The link pages: join, apply, judge, co-organiser and team links. They stand
// alone (no event frame) and work signed out.
import type { ScreenDef } from "@/routes/registry";

export const screens: Record<string, ScreenDef> = {
  "public-join": { kind: "public", title: "Join", load: () => import("./Join") },
  "public-apply": { kind: "public", title: "Apply", load: () => import("./Apply") },
  "public-judge": { kind: "public", title: "Judge invite", load: () => import("./Judge") },
  "public-organiser-invite": { kind: "public", title: "Co-organise this event", load: () => import("./OrganiserInvite") },
  "public-team-link": { kind: "public", title: "Team link", load: () => import("./TeamLink") },
};
