import { beforeEach, describe, expect, it } from "vitest";
import { ApiError } from "@kit/api/call";
import { companyCopy, consumeNext, digits, emailNonceHash, isGmail, landing, message, methods, nonceHash, readNext, takeNonceFor, EMAIL_NONCE, GOOGLE_NONCE } from "./signin";

// The browser parts the logic reads (the tests run in node).
class MemoryStorage {
  private m = new Map<string, string>();
  getItem = (k: string) => this.m.get(k) ?? null;
  setItem = (k: string, v: string) => void this.m.set(k, String(v));
  removeItem = (k: string) => void this.m.delete(k);
  clear = () => this.m.clear();
}
const g = globalThis as Record<string, unknown>;
g.localStorage = new MemoryStorage();
g.sessionStorage = new MemoryStorage();
g.location = new URL("https://simple-hack.app/signin");

beforeEach(() => {
  localStorage.clear();
  sessionStorage.clear();
});

describe("sign-in refusals", () => {
  const refusal = (status: number, body: Record<string, unknown>) => new ApiError(status, String(body.code || ""), String(body.error || "error"), body);
  it("says how many tries are left", () => {
    expect(message(refusal(401, { reason: "code_wrong", tries_left: 2 }))).toBe("That code is not right. 2 tries left.");
    expect(message(refusal(401, { reason: "code_wrong", tries_left: 1 }))).toBe("That code is not right. 1 try left.");
    expect(message(refusal(401, { reason: "code_wrong" }))).toBe("That code is not right.");
  });
  it("words expiry with and without the minutes", () => {
    expect(message(refusal(401, { reason: "code_expired" }))).toBe("That code has expired or was already used. Send a new code.");
    expect(message(refusal(401, { reason: "code_expired", min: 15 }))).toBe("That code has expired or was already used. Codes last 15 minutes. Send a new code.");
  });
  it("uses the table for known codes and the server's words otherwise", () => {
    expect(message(refusal(401, { code: "bad_credentials", error: "x" }))).toBe("Wrong username or password.");
    expect(message(refusal(400, { code: "invalid_password", error: "Use at least 8 characters." }))).toBe("Use at least 8 characters.");
    expect(message(refusal(403, { code: "account_suspended", error: "Closed by the operator." }))).toBe("Closed by the operator.");
    expect(message(refusal(500, { error: "Database down." }))).toBe("Database down.");
    expect(message(new ApiError(0, "network", "Failed to fetch"))).toBe("Could not reach Simple Hack. Check your connection.");
    expect(message(new Error("Sign-in unavailable. Try again."))).toBe("Something went wrong. Try again.");
  });
});

describe("where sign-in goes afterwards", () => {
  it("keeps ?next across the Google round trip and uses it once", () => {
    expect(readNext("?next=%2Fe%2Fx%2Fteam")).toBe("/e/x/team");
    expect(readNext("")).toBe("/e/x/team");
    expect(consumeNext("")).toBe("/e/x/team");
    expect(readNext("")).toBe("");
  });
  it("refuses another origin", () => {
    expect(readNext("?next=https%3A%2F%2Fevil.example%2F")).toBe("");
    expect(readNext("?next=%2F%2Fevil.example")).toBe("");
  });
  it("opens the one current event, else Your events", () => {
    const home = (r: string, s: string) => `/e/${s}/${r}`;
    expect(landing([{ slug: "a", role: "participant", stage: "building" }, { slug: "b", role: "judge", stage: "archived" }], home)).toBe("/e/a/participant");
    expect(landing([{ slug: "a", role: "participant", stage: "building" }, { slug: "b", role: "judge", stage: "judging" }], home)).toBe("/events");
    expect(landing([], home)).toBe("/events");
  });
});

describe("link nonces", () => {
  it("matches the email nonce once", async () => {
    const hash = await emailNonceHash();
    expect(localStorage.getItem(EMAIL_NONCE)).toBeTruthy();
    const n = await takeNonceFor(hash);
    expect(n).not.toBe("");
    expect(await nonceHash(n)).toBe(hash);
    expect(localStorage.getItem(EMAIL_NONCE)).toBeNull();
    expect(await takeNonceFor(hash)).toBe("");
  });
  it("matches a Google nonce and refuses a stranger's link", async () => {
    sessionStorage.setItem(GOOGLE_NONCE, "g-nonce");
    expect(await takeNonceFor("someone-else")).toBe("");
    expect(await takeNonceFor(await nonceHash("g-nonce"))).toBe("g-nonce");
    expect(sessionStorage.getItem(GOOGLE_NONCE)).toBeNull();
  });
});

describe("what the card offers", () => {
  it("reads the providers answer", () => {
    expect(methods(null)).toEqual({ providers: [], emailOn: true, oidcLabel: "" });
    expect(methods({ providers: ["oidc", "github", "google"], email_enabled: false }).providers).toEqual(["google", "oidc"]);
  });
  it("words a company install", () => {
    expect(companyCopy(methods({ providers: ["oidc"], email_enabled: false, oidc_label: "Acme" }))).toEqual({
      fine: "Email codes are turned off on this server. Ask your IT team if you cannot sign in.",
      lead: "Use your Acme account.",
    });
    expect(companyCopy(methods({ providers: ["google"], email_enabled: true }))).toBeNull();
  });
  it("helps with the code", () => {
    expect(digits("12 34-56 7")).toBe("123456");
    expect(isGmail("A@GoogleMail.com")).toBe(true);
    expect(isGmail("a@example.com")).toBe(false);
  });
});
