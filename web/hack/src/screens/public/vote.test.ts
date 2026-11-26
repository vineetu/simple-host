import { describe, expect, it } from "vitest";
import { ineligible, intro } from "./vote";

describe("vote words", () => {
  it("says who may vote and what is happening", () => {
    expect(ineligible("participants")).toBe("Only approved participants can vote in this event.");
    expect(ineligible("x")).toBe("Your account is not eligible to vote in this event.");
    expect(intro("upcoming")).toBe("Voting opens soon.");
  });
});
