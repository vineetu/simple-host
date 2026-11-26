import { describe, expect, it } from "vitest";
import { eventGroups } from "./events";
import type { EventRow } from "@/lib/client";

const ev = (slug: string, title: string, stage = "open", ended_at?: string) => ({ slug, title, stage, role: "participant", ended_at }) as EventRow;

describe("eventGroups", () => {
  it("puts the open event first, then by title; past by end, latest first", () => {
    const g = eventGroups([ev("b", "Beta"), ev("a", "alpha"), ev("z", "Zed"), ev("o1", "Old", "archived", "2026-01-01T00:00:00Z"), ev("o2", "Older", "archived", "2026-03-01T00:00:00Z")], "z");
    expect(g.current.map((e) => e.slug)).toEqual(["z", "a", "b"]);
    expect(g.past.map((e) => e.slug)).toEqual(["o2", "o1"]);
    expect(g.pastCount).toBe(2);
  });
});
