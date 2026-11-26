// The link pages (join, apply, judge, co-organiser and team links), sign-in,
// Find a hackathon and People's choice. They stand alone (no event frame) and work signed out.
// Find a hackathon is registered but not live: /directory stays the indexed server-rendered page.
import type { ScreenDef } from "@/routes/registry";

export const screens: Record<string, ScreenDef> = {
  "public-join": { kind: "public", title: "Join", load: () => import("./Join") },
  "public-apply": { kind: "public", title: "Apply", load: () => import("./Apply") },
  "public-judge": { kind: "public", title: "Judge invite", load: () => import("./Judge") },
  "public-organiser-invite": { kind: "public", title: "Co-organise this event", load: () => import("./OrganiserInvite") },
  "public-team-link": { kind: "public", title: "Team link", load: () => import("./TeamLink") },
  signin: { kind: "public", title: "Sign in", load: () => import("./SignIn") },
  "public-directory": { kind: "public", title: "Find a hackathon", load: () => import("./Directory") },
  "public-vote": { kind: "public", title: "People’s choice", load: () => import("./Vote") },
};
