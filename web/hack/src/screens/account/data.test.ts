import { describe, expect, it } from "vitest";
import { accountErr, ago, canOpen, connectionRows, createErrors, identityLine, nonceHash, slugify, stepsFor, words } from "./data";
import { zoneAbbr, zoneOptions } from "@/lib/time";
import { ApiError } from "@kit/api/call";

const form = { title: "T", slug: "t-1", start: "2027-01-10", end: "", tz: "UTC", organiser: "A", org: "", email: "a@example.com", purpose: "p", n: "60" };

describe("Account", () => {
  it("names the account as the old screen did", () => {
    expect(words({ username: "u-1234abcd", login: "siti", email: "siti@example.com", handle: "siti-h" })).toEqual({ email: "siti", confirm: "siti-h", realEmail: "siti@example.com", login: "siti" });
    expect(words({ username: "ayu@example.com" }).realEmail).toBe("ayu@example.com");
    expect(canOpen({ username: "admin" })).toBe(false);
    expect(canOpen({ email: "a@b.co", team: "x" })).toBe(false);
    expect(canOpen({ email: "a@b.co" })).toBe(true);
  });
  it("keeps internal names out of refusals", () => {
    const w = words({ email: "a@b.co", handle: "hh" });
    expect(accountErr(new ApiError(409, "x", "hh organises u-abcdef12's event"), w)).toBe("your account organises your account's event");
    expect(accountErr(new ApiError(0, "offline", "Cannot reach the server."), w)).toBe("Cannot reach the server.");
    expect(accountErr(new Error("no key"), w, "Could not save")).toBe("Could not save");
  });
  it("says how long ago", () => {
    const now = Date.parse("2026-11-20T10:00:00Z");
    expect(ago("2026-11-20T09:59:30Z", now)).toBe("just now");
    expect(ago("2026-11-20T09:59:00Z", now)).toBe("1 minute ago");
    expect(ago("2026-11-20T07:00:00Z", now)).toBe("3 hours ago");
    expect(ago("2026-11-18T10:00:00Z", now)).toBe("2 days ago");
    expect(ago(undefined, now)).toBe("");
  });
  it("lists apps newest first, naming devices only when two share a name", () => {
    const now = Date.parse("2026-11-20T10:00:00Z");
    const rows = connectionRows(
      [
        { client_id: "a", name: "Claude", device: "Mac", last_used_at: "2026-11-19T10:00:00Z", teams: [{ team: "Lumbung", event: "Jakarta AI Night" }] },
        { client_id: "b", name: "Claude", device: "Phone", last_used_at: "2026-11-20T09:00:00Z" },
        { client_id: "c", name: "ChatGPT", device: "Web", teams: [{ team: "A", event: "E" }, { team: "B", event: "E" }, { team: "C", event: "F" }] },
      ],
      now,
    );
    expect(rows.map((r) => r.name)).toEqual(["Claude (Phone)", "Claude (Mac)", "ChatGPT"]);
    expect(rows[0].line).toBe("Last used 1 hour ago · Hasn't picked a team yet");
    expect(rows[1].line).toBe("Last used 1 day ago · Publishes to Lumbung in Jakarta AI Night");
    expect(rows[2].line).toBe("Publishes to A in E, B in E and C in F");
  });
  it("words a linked sign-in", () => {
    expect(identityLine({ email: "s@gmail.com" }, "Google", "3 Nov")).toBe("Google sign-in: s@gmail.com, linked 3 Nov. This Google account can sign in to yours.");
  });
  it("uses the hosted directory path only on the hosted instance", () => {
    const app = { id: "claude", label: "Claude", connect: [{ html: "a", address: true }], connect_hosted: [{ html: "h" }], fallback: "f" };
    expect(stepsFor(app, "https://hack.example.org")).toEqual([{ html: "a", address: true }]);
    expect(stepsFor(app, "https://simple-hack.app")).toEqual([{ html: "h", fallback: "f" }]);
  });
  it("hashes the sign-in nonce as base64url SHA-256", async () => {
    expect(await nonceHash("abc")).toBe("ungWv48Bz-pBQUDeXa4iI7ADYaOWF3qctBD_YfIAFa0");
  });
});

describe("Create event", () => {
  it("makes an address from the title", () => {
    expect(slugify("Generation Girl Online Hack 2026!")).toBe("generation-girl-online-hack-2026");
    expect(slugify("  --A--  ")).toBe("a");
    expect(slugify("a".repeat(38) + " b c")).toBe("a".repeat(38));
  });
  it("checks the form field by field", () => {
    expect(createErrors(form)).toEqual({});
    expect(Object.keys(createErrors({ ...form, title: " ", slug: "", start: "", organiser: "", email: "x", purpose: "", n: "0" }))).toEqual(["title", "slug", "start", "organiser", "email", "purpose", "n"]);
    expect(createErrors({ ...form, end: "2027-01-01" }).end).toBe("It can't end before it starts.");
    expect(createErrors({ ...form, n: "100001" }).n).toBe("Expected participants must be between 1 and 100000.");
  });
  it("labels time zones with their short name and offset", () => {
    const d = new Date("2026-11-20T10:00:00Z");
    expect(zoneAbbr("Asia/Jakarta", d)).toBe("WIB");
    expect(zoneAbbr("Europe/Berlin", d)).toBe("CET");
    expect(zoneAbbr("Australia/Sydney", d)).toBe("AEDT");
    expect(zoneOptions("Asia/Jakarta", d).find((z) => z.zone === "Asia/Jakarta")?.label).toBe("Asia/Jakarta · WIB, UTC+7");
    expect(zoneOptions("Mars/Base", d).some((z) => z.zone === "Mars/Base")).toBe(true);
  }, 30000);
});
