import { describe, expect, it } from "vitest";
import { leaveItem, nextAction } from "./data";
import type { EventRow } from "@/lib/client";

const row = (o: Partial<EventRow>) => ({ slug: "x", title: "X", stage: "open", role: "participant", ...o }) as EventRow;

describe("Your events rows", () => {
  it("says the next step in plain words", () => {
    expect(nextAction(row({ role: "organiser", stage: "draft" }))).toBe("Finish setting up");
    expect(nextAction(row({ approval_status: "pending" }))).toBe("Waiting for the organisers");
    expect(nextAction(row({ role: "judge", stage: "judging" }))).toBe("Score teams");
    expect(nextAction(row({ taken_down: true }))).toBe("");
  });
  it("offers leaving only when it can happen, ended outranking pending", () => {
    expect(leaveItem(row({ stage: "archived", approval_status: "pending" }))).toEqual({ label: "Can't leave, the event has ended", disabled: true });
    expect(leaveItem(row({ approval_status: "pending" }))).toEqual({ label: "Withdraw application…" });
    expect(leaveItem(row({ role: "organiser" }))).toEqual({ label: "Leave this event…" });
  });
});
