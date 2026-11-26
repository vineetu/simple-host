import { describe, expect, it } from "vitest";
import { blocks, capMessages, formatResetDate, inline, limitLine, normaliseHref, readSSE, sse, starterChips, storageKey, type Msg, type StreamEvent } from "./data-assistant";

describe("assistant", () => {
  it("keys history by person and event", () => {
    expect(storageKey("ev", "u1")).toBe("sh-assistant-u1-ev");
    expect(storageKey("ev", null)).toBe("sh-assistant-ev");
  });

  it("trims to 20 messages, starting on a question", () => {
    const many: Msg[] = Array.from({ length: 25 }, (_, i) => ({ role: i % 2 ? "assistant" : "user", content: "m" + i }));
    const out = capMessages(many);
    expect(out.length).toBeLessThanOrEqual(20);
    expect(out[0].role).toBe("user");
    const long: Msg[] = [
      { role: "user", content: "x".repeat(11000) },
      { role: "assistant", content: "y".repeat(2000) },
      { role: "user", content: "z" },
    ];
    expect(capMessages(long)).toEqual([{ role: "user", content: "z" }]);
  });

  it("links only into the event or the skills", () => {
    expect(inline("[Teams](/e/ev/manage?s=teams)", "ev")).toEqual([{ t: "a", text: "Teams", href: "/e/ev/manage?s=teams" }]);
    expect(inline("[x](https://evil.example/)", "ev")).toEqual([{ t: "text", text: "x" }]);
    expect(inline("[x](/e/other/manage)", "ev")).toEqual([{ t: "text", text: "x" }]);
    expect(inline("[x](/e/ev/../../admin)", "ev")).toEqual([{ t: "text", text: "x" }]);
    expect(normaliseHref("//e/ev/ manage")).toBe("/e/ev/manage");
  });

  it("reads paragraphs, lists and code", () => {
    const b = blocks("Hi **there**\nline two\n\n- a\n- b\n\n1. one\n```\ncode\n```", "ev");
    expect(b.map((x) => x.t)).toEqual(["p", "ul", "ol", "pre"]);
    expect(b[0]).toEqual({ t: "p", lines: [[{ t: "text", text: "Hi " }, { t: "strong", text: "there" }], [{ t: "text", text: "line two" }]] });
  });

  it("starts with three different questions", () => {
    expect(starterChips("building", { next: { label: "Close entries" } })).toEqual([
      "What happens when I close entries?",
      'What does "Close entries" mean?',
      "Where do I find the join and judge links?",
    ]);
    expect(starterChips("draft", null)[1]).toBe("What should I do next?");
  });

  it("says the limits", () => {
    expect(formatResetDate("2026-12-01")).toBe("1 December");
    expect(limitLine({ budget_reached: true, resets: "2026-12-01" })).toBe("The assistant has used this month's budget. It's back on 1 December.");
    expect(limitLine({ remaining_today: 0, daily_limit: 30 })).toBe("You've asked 30 questions today. The limit resets at midnight UTC.");
    expect(limitLine({ remaining_today: 3 })).toBe("");
  });

  it("reads the stream", async () => {
    const got: StreamEvent[] = [];
    await readSSE(sse([{ delta: "Hel" }, { delta: "lo" }, { done: true, remaining_today: 4 }]).body!, (e) => got.push(e));
    expect(got).toEqual([{ delta: "Hel" }, { delta: "lo" }, { done: true, remaining_today: 4 }]);
  });
});
