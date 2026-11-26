import { describe, expect, it } from "vitest";
import { ApiError } from "@kit/api/call";
import { APPLY_ERRORS, COUNTRY_COUNT, accountName, applyDates, applyField, checkJoin, closedLine, errText, joinBody, memberLine, teamLinkCase, unanswered } from "./data";
import { zoneAbbr } from "@/lib/time";

describe("link pages", () => {
  it("say a refusal in this page's words, else the server's", () => {
    expect(errText(new ApiError(429, "rate_limited", "x"), APPLY_ERRORS)).toBe("Too many applications from this account. Wait a while and try again.");
    expect(errText(new ApiError(400, "odd", "The server's own line."))).toBe("The server's own line.");
    expect(errText(new ApiError(0, "offline", ""))).toBe("Cannot reach the server.");
  });
  it("ask for a name only when the account has none, and send it only then", () => {
    expect(accountName(" Siti ", { display_name: "Other" })).toBe("Siti");
    expect(accountName(undefined, { display_name: "Siti" })).toBe("Siti");
    expect(checkJoin({ coc: false, askName: true, name: " " })).toEqual({ coc: "Please agree to the code of conduct.", name: "Enter your name." });
    expect(checkJoin({ coc: true, askName: false, name: "" })).toEqual({});
    expect(joinBody({ askName: false, name: "x" })).toEqual({ accept_coc: true });
    expect(joinBody({ askName: true, name: " Ayu ", answers: { q: "a" } })).toEqual({ accept_coc: true, answers: { q: "a" }, display_name: "Ayu" });
    expect(unanswered([{ id: "a", prompt: "A", required: true }, { id: "b", prompt: "B" }], { a: " " })).toEqual({ a: "Answer this question." });
  });
  it("name why a link is closed and what a member sees", () => {
    expect(closedLine("draft", "join")).toBe("This event is not open yet.");
    expect(closedLine("results", "judge")).toBe("This judge invite isn’t open any more.");
    expect(closedLine("building", "join")).toBe("Sign-up has closed for this event.");
    expect(memberLine("pending", "X").open).toBe("page");
    expect(memberLine("approved", "X")).toEqual({ text: "You're already in X.", tone: "info", open: "team" });
  });
  it("label zones and date ranges as the event page does", () => {
    expect(zoneAbbr("Asia/Jakarta")).toBe("WIB");
    expect(applyDates("2026-12-05T00:00:00+07:00", "2026-12-06T00:00:00+07:00", "Asia/Jakarta")).toBe("5–6 Dec 2026");
    expect(applyDates("2026-11-30T00:00:00Z", "2026-12-01T00:00:00Z", "UTC")).toBe("30 Nov–1 Dec 2026");
    expect(COUNTRY_COUNT).toBe(250);
    expect(applyField("invalid_country")).toBe("country");
    expect(applyField("rate_limited")).toBe("");
  });
  it("pick the team link's answer in the old order", () => {
    expect(teamLinkCase({}, false)).toBe("signed-out");
    expect(teamLinkCase({ you: { status: "organiser" } }, true)).toBe("staff");
    expect(teamLinkCase({ you: { status: "on_other_team" }, reason: "full" }, true)).toBe("on_other_team");
    expect(teamLinkCase({ you: { status: "ok" }, reason: "full" }, true)).toBe("full");
    expect(teamLinkCase({ you: { status: "ok" } }, true)).toBe("ready");
  });
});
