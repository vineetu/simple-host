// The link pages and sign-in: they stand alone, signed in or out.
import type { ScreenDef } from "@/routes/registry";

export const screens: Record<string, ScreenDef> = {
  signin: { kind: "public", title: "Sign in", load: () => import("./SignIn") },
  "public-directory": { kind: "public", title: "Find a hackathon", load: () => import("./Directory") },
  "public-vote": { kind: "public", title: "People’s choice", load: () => import("./Vote") },
};
