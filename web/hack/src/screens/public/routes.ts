// The link pages and sign-in: they stand alone, signed in or out.
import type { ScreenDef } from "@/routes/registry";

export const screens: Record<string, ScreenDef> = {
  signin: { kind: "public", title: "Sign in", load: () => import("./SignIn") },
};
