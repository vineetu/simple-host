// The judge's area: /e/{slug}/judge?s=home|queue|rubric|scoring (a full page load, for its own frame policy).
import type { ScreenDef } from "@/routes/registry";

export const screens: Record<string, ScreenDef> = {
  "judge-home": { kind: "event", title: "Overview", load: () => import("./Home") },
  "judge-queue": { kind: "event", title: "My teams", load: () => import("./Queue") },
  "judge-scoring": { kind: "event", title: "Score entry", load: () => import("./Scoring") },
  "judge-rubric": { kind: "event", title: "Rubric & brief", load: () => import("./Rubric") },
};
