import { describe, expect, it } from "vitest";
import { ApiError } from "@kit/api/call";
import { archiveError, fill, hookLine, prompt, siteLine, strip, testLine } from "./data";

const NOW = Date.parse("2026-11-20T10:00:00Z");

describe("Website strip", () => {
  it("names every connected app and teammates", () => {
    const s = strip({ ready: true, exists: false, url: "https://t.e.simple-hack.app/" }, { state: "connected", apps: ["Claude", "Bolt"], last_used_at: "2026-11-20T09:56:00Z", teammates_connected: ["Dian"] }, false, NOW);
    expect(s.app).toMatchObject({ on: "on", label: "Connected in Claude and Bolt", sub: "Last used 4 minutes ago · Dian is connected too" });
    expect(s.web).toMatchObject({ on: "wait", label: "Not published yet", sub: "Your address is ready: t.e.simple-hack.app/" });
  });
  it("shows setting up, taken down, published and the pinned version after the deadline", () => {
    const none = { state: "none" };
    expect(strip({ ready: false }, none, false, NOW).web.label).toBe("Setting up");
    expect(strip({ ready: true, taken_down: true }, none, false, NOW).web.sub).toBe("Taken down by Simple Host.");
    expect(strip({ ready: true, exists: true, live_version: 3, published_at: "2026-11-20T09:48:00Z" }, none, false, NOW).web).toMatchObject({ label: "Published · version 3", sub: "12 minutes ago" });
    expect(strip({ ready: true, pinned_version: 2 }, none, true, NOW).web.label).toBe("Submitted version 2");
    expect(strip({ ready: true }, none, true, NOW).app.sub).toBe("Publishing is closed.");
  });
  it("says who published and how", () => {
    expect(siteLine({ live_version: 3, published_via: "Claude", published_at: "2026-11-20T08:00:00Z" }, NOW)).toBe("Version 3, published via Claude, 2 hours ago. Judges see the version that is live at your deadline.");
  });
});

describe("Website words", () => {
  it("builds the prompt from the entry", () => {
    expect(prompt("Jakarta AI Night", "Lumbung", { problem: "Co-ops lose track", tagline: "A ledger." })).toBe(
      "Build a one-page website for our hackathon project and publish it as our team site for Jakarta AI Night (team Lumbung) on Simple Hack.\nProblem: Co-ops lose track.\nSolution: A ledger.",
    );
  });
  it("turns the extractor's refusals into plain words", () => {
    expect(archiveError(new ApiError(400, "missing_index", "no index.html at the root of the archive"), "x")).toBe("Put index.html at the top of the zip, not inside a folder.");
    expect(archiveError(new ApiError(400, "invalid_archive", "invalid site archive: unsafe path ../x"), "x")).toMatch(/file paths/);
    expect(archiveError(new ApiError(400, "invalid_archive", "gzip: invalid header"), "x")).toBe("That file isn't a valid .zip.");
  });
  it("escapes the person's own words inside trusted step markup", () => {
    expect(fill("Sign in with {email} in {app}", "Claude", "<x>@y")).toBe("Sign in with <b>&lt;x&gt;@y</b> in Claude");
  });
  it("describes a notification and a test", () => {
    expect(hookLine({ id: "1", events: ["record.added"], state: "paused" })).toBe("new entries · all saved data · Paused · No deliveries yet");
    expect(testLine({ delivered: false, result: "timeout", status: 0 })).toBe("Receiver took too long.");
  });
});
