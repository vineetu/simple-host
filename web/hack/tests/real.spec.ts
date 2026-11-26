// The real simple-hack binary, clicked through the rebuilt screens at 390 and
// 1280 px: built from this checkout, on a throwaway database in the test
// Postgres the owner app's real check uses (Docker container sh-next-test,
// 127.0.0.1:54329), with password sign-in on so people can be made by script.
// The browser's https requests for hack.test are answered by the binary.
// Pictures land in test-results/real/ to be looked at; they are not baselines.
import { execFileSync, spawn, type ChildProcess } from "node:child_process";
import { mkdirSync, mkdtempSync, openSync, readFileSync, rmSync, copyFileSync } from "node:fs";
import http from "node:http";
import net from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { expect as baseExpect, test, type BrowserContext, type Page } from "@playwright/test";
import { axe, layout } from "../../kit/tests/checks";

test.describe.configure({ mode: "serial", timeout: 600_000 });
// The box running this may be busy: give each screen longer than the default 5 s to appear.
const expect = baseExpect.configure({ timeout: 30_000 });

const ROOT = join(import.meta.dirname, "../../..");
const SHOTS = join(import.meta.dirname, "../test-results/real");
const PG = "sh-next-test";
const DB = "hk_" + Date.now().toString(36);
const HOST = "hack.test";
const APP = "https://" + HOST;
const SLUG = "real-night";
let PORT = 0;
let server: ChildProcess;
let dir: string;
const keys: Record<string, string> = {};

function psql(sql: string, db = "postgres") {
  return execFileSync("docker", ["exec", PG, "psql", "-U", "postgres", "-d", db, "-tAc", sql], { encoding: "utf8" });
}

/** call is one request straight to the binary, as a script would make it. */
function call(method: string, path: string, body?: unknown, key = ""): Promise<{ status: number; body: Record<string, unknown> }> {
  return new Promise((resolve, reject) => {
    const data = body === undefined ? undefined : JSON.stringify(body);
    const headers: Record<string, string> = { host: HOST, "x-forwarded-proto": "https", "content-type": "application/json" };
    if (key) headers["x-api-key"] = key;
    const req = http.request({ host: "127.0.0.1", port: PORT, method, path, headers }, (res) => {
      let b = "";
      res.on("data", (c) => (b += c));
      res.on("end", () => resolve({ status: res.statusCode!, body: b.startsWith("{") || b.startsWith("[") ? JSON.parse(b) : { text: b } }));
    });
    req.on("error", reject);
    req.end(data);
  });
}

async function ok(method: string, path: string, body: unknown, key: string) {
  const r = await call(method, path, body, key);
  expect(r.status, `${method} ${path}: ${JSON.stringify(r.body)}`).toBeLessThan(300);
  return r.body;
}

function freePort(): Promise<number> {
  return new Promise((resolve) => {
    const s = net.createServer().listen(0, "127.0.0.1", () => {
      const p = (s.address() as net.AddressInfo).port;
      s.close(() => resolve(p));
    });
  });
}

async function route(ctx: BrowserContext) {
  await ctx.route(/^https:\/\/([^/]*\.)?hack\.test(\/|$)/, async (r) => {
    const req = r.request();
    const u = new URL(req.url());
    const headers = { ...(await req.allHeaders()), host: u.host, "x-forwarded-proto": "https" };
    delete (headers as Record<string, string>)["accept-encoding"];
    const res = await new Promise<{ status: number; headers: Record<string, string>; body: Buffer }>((resolve, reject) => {
      const p = http.request({ host: "127.0.0.1", port: PORT, method: req.method(), path: u.pathname + u.search, headers }, (res) => {
        const chunks: Buffer[] = [];
        res.on("data", (c) => chunks.push(c));
        res.on("end", () => {
          const h: Record<string, string> = {};
          for (const [k, v] of Object.entries(res.headers)) if (v !== undefined) h[k] = Array.isArray(v) ? v.join("\n") : v;
          resolve({ status: res.statusCode!, headers: h, body: Buffer.concat(chunks) });
        });
      });
      p.on("error", reject);
      p.end(req.postDataBuffer() ?? undefined);
    });
    await r.fulfill(res).catch(() => {});
  });
}

async function signUp(name: string) {
  const r = await ok("POST", "/v1/auth/password/signup", { username: name, password: "correct-horse-" + name }, "");
  keys[name] = String(r.api_key);
}

test.beforeAll(async () => {
  test.setTimeout(600_000);
  PORT = await freePort();
  dir = mkdtempSync(join(tmpdir(), "hk-fe-"));
  execFileSync("go", ["build", "-o", join(dir, "simple-hack"), "./cmd/simple-hack"], { cwd: ROOT, stdio: "inherit" });
  psql(`CREATE DATABASE ${DB}`);
  server = spawn(join(dir, "simple-hack"), [], {
    env: {
      ...process.env,
      DB_DSN: `postgres://postgres:test@127.0.0.1:54329/${DB}?sslmode=disable`,
      MIGRATE_ON_START: "true",
      PORT: String(PORT),
      DATA_DIR: join(dir, "data"),
      PUBLIC_BASE_URL: APP,
      SITE_DOMAIN: HOST,
      PASSWORD_SIGNIN: "on",
      ADMIN_API_KEY: "real-test-admin-key-0123456789abcdef",
      PASSCODE_ENC_KEY: Buffer.alloc(32, 7).toString("base64"),
      RESEND_API_KEY: "",
    },
    stdio: ["ignore", openSync(join(dir, "server.log"), "w"), openSync(join(dir, "server.log"), "a")],
  });
  for (let i = 0; i < 1200; i++) {
    try {
      if ((await call("GET", "/events")).status === 200) break;
    } catch {
      /* not up yet */
    }
    await new Promise((r) => setTimeout(r, 150));
  }
  if (server.exitCode !== null) throw new Error("the service stopped: " + readFileSync(join(dir, "server.log"), "utf8"));

  // An organiser runs one event to judging; Siti is on a team with a submitted entry.
  await signUp("orga");
  await signUp("siti");
  await signUp("newbie");
  const E = "/v1/hack/events/" + SLUG;
  await ok("POST", "/v1/hack/events", { slug: SLUG, title: "Real Night", organiser_name: "Orga", contact_email: "orga@example.com", purpose: "Testing", time_zone: "UTC", expected_participants: 20, starts_at: "2027-01-10", ends_at: "2027-01-11" }, keys.orga);
  await ok("POST", E + "/stage", { stage: "open" }, keys.orga);
  const ev = await ok("GET", E, undefined, keys.orga);
  const joinURL = String((ev.organiser as Record<string, unknown>).join_url);
  const code = joinURL.split("/").pop()!;
  await ok("POST", "/v1/hack/join/" + code, { accept_coc: true, display_name: "Siti" }, keys.siti);
  await ok("POST", E + "/teams", { name: "Lumbung" }, keys.siti);
  await ok("PUT", E + "/entry", { title: "Rice Ledger", tagline: "A shared ledger farmers keep by voice message.", problem: "Co-ops lose track.", audience: "Rice co-ops", tools: ["Claude"], maturity: "working", video_url: "https://youtu.be/dQw4w9WgXcQ" }, keys.siti);
  await ok("POST", E + "/entry/submit", {}, keys.siti);
  for (const stage of ["building", "closed", "judging"]) await ok("POST", E + "/stage", { stage }, keys.orga);
});

test.afterAll(async () => {
  server?.kill();
  if (dir) {
    mkdirSync(SHOTS, { recursive: true });
    copyFileSync(join(dir, "server.log"), join(SHOTS, "server.log"));
  }
  try {
    psql(`DROP DATABASE IF EXISTS ${DB} WITH (FORCE)`);
  } catch {
    /* already gone */
  }
  if (dir) rmSync(dir, { recursive: true, force: true });
});

async function check(page: Page, name: string, width: number) {
  const problems = [...(await layout(page)), ...(await axe(page))];
  mkdirSync(SHOTS, { recursive: true });
  await page.screenshot({ path: `${SHOTS}/${name}-${width}.png`, fullPage: true });
  expect(problems, name).toEqual([]);
}

async function person(browser: import("@playwright/test").Browser, who: string, width: number) {
  const ctx = await browser.newContext({ viewport: { width, height: 900 } });
  await route(ctx);
  if (who) await ctx.addInitScript((k) => localStorage.setItem("apiKey", k), keys[who]);
  const page = await ctx.newPage();
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("console", (m) => m.type() === "error" && !/Failed to load resource/.test(m.text()) && errors.push(m.text()));
  return { ctx, page, errors };
}

for (const width of [390, 1280]) {
  test(`your events and submissions, the new app @${width}`, async ({ browser }) => {
    const { ctx, page, errors } = await person(browser, "siti", width);
    // Your events is the new app: the server says so, and it renders Siti's event.
    await page.goto(APP + "/events");
    await expect(page.locator('meta[name="hack-live"]')).toHaveAttribute("content", /landing-your-events/);
    await expect(page.getByRole("heading", { name: "Your events", level: 1 })).toBeVisible();
    await expect(page.getByRole("link", { name: /Real Night/ }).last()).toBeVisible();
    await check(page, "your-events", width);

    // The SSO cookie call went out on load (team and event sites sign in from it).
    const cookies = await ctx.cookies(APP);
    expect(cookies.some((c) => c.name.includes("sso")), "SSO cookie set: " + cookies.map((c) => c.name).join(",")).toBe(true);

    // Submissions: the new app inside the event frame, with Siti's entry.
    await page.goto(APP + `/e/${SLUG}/team?s=submissions`);
    await expect(page.getByRole("heading", { name: "Submissions", level: 1 })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Rice Ledger" })).toBeVisible();
    await expect(page.getByText("Your team")).toBeVisible();
    await check(page, "submissions", width);

    // Every app address is the new app now: the server answers index.html, never the old app.html.
    // The only addresses left to the old pages are the server-written ones: /directory (kept
    // as its own indexed page on purpose), the home page and the public event page.
    const areas: Record<string, string[]> = {
      manage: ["overview", "event-page", "announcements", "people", "teams", "team", "entries", "entry", "judging", "assignments", "results", "settings"],
      team: ["home", "schedule", "announcements", "team", "entry", "website", "results", "submissions"],
      judge: ["home", "queue", "scoring", "rubric"],
    };
    const addresses = ["/events", "/events/new", "/account", "/signin", `/e/${SLUG}/apply`, `/e/${SLUG}/vote`, "/join/x", "/judge/x", "/organiser/x", "/team/x"];
    for (const [area, ss] of Object.entries(areas)) for (const s of ss) addresses.push(`/e/${SLUG}/${area}?s=${s}`);
    for (const a of addresses) {
      const html = await (await page.request.get(APP + a)).text();
      expect(html, a).toContain('<div id="root">');
      expect(html, a).not.toContain('src="/hack/app.js"');
    }
    const directory = await (await page.request.get(APP + "/directory")).text();
    expect(directory).not.toContain('<div id="root">');

    expect(errors).toEqual([]);
    await ctx.close();
  });
}

test("leaving an event from Your events (the only organiser deletes it), and signed out goes to sign-in", async ({ browser }) => {
  await ok("POST", "/v1/hack/events", { slug: "solo-night", title: "Solo Night", organiser_name: "New", contact_email: "new@example.com", purpose: "Testing", time_zone: "UTC", expected_participants: 20, starts_at: "2027-01-10", ends_at: "2027-01-11" }, keys.newbie);
  const { ctx, page, errors } = await person(browser, "newbie", 1280);
  await page.goto(APP + "/events");
  await page.getByRole("button", { name: "More for Solo Night" }).click();
  await page.getByRole("menuitem", { name: "Leave this event…" }).click();
  await expect(page.getByRole("dialog", { name: "You're the only organiser" })).toBeVisible();
  await page.getByRole("radio", { name: "Delete the event" }).click();
  const go = page.getByRole("button", { name: "Delete event and leave" });
  await expect(go).toBeDisabled();
  await page.getByLabel("Type solo-night to confirm").fill("solo-night");
  await check(page, "leave-last-organiser", 1280);
  await go.click();
  await expect(page.getByText("Solo Night was deleted.").first()).toBeVisible();
  await expect(page.getByText("You’re not part of any event yet")).toBeVisible();
  expect((await call("GET", "/v1/hack/events", undefined, keys.newbie)).body).toEqual([]);
  expect(errors).toEqual([]);
  await ctx.close();

  const out = await person(browser, "", 390);
  await out.page.goto(APP + "/events");
  await out.page.waitForURL(/\/signin\?next=%2Fevents/);
  await out.ctx.close();
});

for (const width of [390, 1280]) {
  test(`the public event page and the home page, on the stylesheets built from the tokens @${width}`, async ({ browser }) => {
    const { ctx, page, errors } = await person(browser, "", width);
    const styles: string[] = [];
    page.on("response", (r) => /\/hack-(event|pages)\.css/.test(r.url()) && r.status() === 200 && styles.push(new URL(r.url()).pathname));
    // The event page: server-written, at the event's stage (judging), with its submission.
    await page.goto(APP + `/e/${SLUG}`);
    await expect(page.getByRole("heading", { name: "Real Night", level: 1 })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Submissions" })).toBeVisible();
    await expect(page.getByText("Rice Ledger").first()).toBeVisible();
    await expect(page.locator('link[href*="/hack/kit.css"]')).toHaveCount(0);
    await check(page, "event-page", width);
    // Home: the chrome's stylesheet is Hack's, built from the tokens; no hand font in the words.
    await page.goto(APP + "/");
    await expect(page.locator("html")).toHaveClass(/sh-hack/);
    const h1 = await page.locator("h1").first().evaluate((el) => getComputedStyle(el).fontFamily);
    expect(h1).not.toMatch(/Caveat|Kalam/);
    await check(page, "home", width);
    expect(styles.sort()).toEqual(["/hack-event.css", "/hack-pages.css"]);
    expect(errors).toEqual([]);
    await ctx.close();
  });
}
// Organiser setup (D2): the organiser edits the event page and their details, then reads Judging and the grid.
for (const width of [390, 1280]) {
  test(`organiser setup: event page, settings, judging and the grid @${width}`, async ({ browser }) => {
    test.setTimeout(360_000);
    const { ctx, page, errors } = await person(browser, "orga", width);
    const E = "/v1/hack/events/" + SLUG;

    await page.goto(APP + `/e/${SLUG}/manage?s=event-page`);
    await expect(page.locator('meta[name="hack-live"]')).toHaveAttribute("content", /organiser-event-page/);
    await expect(page).toHaveURL(/tab=basics/);
    const tagline = "Tagline at " + width;
    await page.getByLabel("Tagline (optional)").fill(tagline);
    await page.getByRole("button", { name: "Save", exact: true }).click();
    // A save reads the event again; on this test server GET E takes seconds (its site-certificate check), so allow for it.
    await expect(page.getByText("No unsaved changes")).toBeVisible({ timeout: 90_000 });
    await expect.poll(async () => ((await call("GET", E, undefined, keys.orga)).body.event as Record<string, unknown>).tagline).toBe(tagline);
    await check(page, "event-page-basics", width);

    // An old #hash link lands on its tab.
    await page.goto(APP + `/e/${SLUG}/manage?s=event-page#appearance`);
    await expect(page).toHaveURL(/tab=look/);

    await page.goto(APP + `/e/${SLUG}/manage?s=settings&tab=details`);
    await expect(page.getByLabel("Organisation or community")).toBeVisible();
    await page.getByLabel("Organisation or community").fill("Real Club " + width);
    await page.getByRole("button", { name: "Save", exact: true }).click();
    // A save reads the event again; on this test server GET E takes seconds (its site-certificate check), so allow for it.
    await expect(page.getByText("No unsaved changes")).toBeVisible({ timeout: 90_000 });
    await expect.poll(async () => ((await call("GET", E, undefined, keys.orga)).body.organiser as Record<string, unknown>).organisation).toBe("Real Club " + width);
    await check(page, "settings-details", width);

    await page.goto(APP + `/e/${SLUG}/manage?s=judging`);
    await expect(page.getByRole("heading", { name: "Judging", level: 1 })).toBeVisible();
    await expect(page.getByText("Lumbung").filter({ visible: true }).first()).toBeVisible();
    await check(page, "judging", width);

    await page.getByRole("link", { name: "Open the assignment grid" }).click();
    await expect(page.getByRole("heading", { name: "Assignment grid", level: 1 })).toBeVisible();
    await expect(page.getByRole("button", { name: "Save judging access" })).toBeDisabled();
    await check(page, "assignments", width);
    expect(errors).toEqual([]);
    await ctx.close();
  });
}

// The judge's area (phase C): a judge joins by the judge link, opens Overview
// (its walkthrough opens once and is skipped), scores the one team through the
// chips and a comment, and lands on My teams with it scored; the scores are on
// the server. Coming into Scoring from a screen loaded elsewhere loads the page
// again, so the judge address's frame policy applies.
// A loaded box can take a few seconds per screen, so this walk waits longer than the default.
const slow = expect.configure({ timeout: 30_000 });

test("judging: Overview, scoring a team, My teams", async ({ browser }) => {
  test.setTimeout(600_000);
  await signUp("budi");
  const E = "/v1/hack/events/" + SLUG;
  const ev = await ok("GET", E, undefined, keys.orga);
  const code = String((ev.organiser as Record<string, unknown>).judge_url).split("/").pop()!;
  await ok("POST", "/v1/hack/judge/" + code, { accept_coc: true, display_name: "Budi" }, keys.budi);

  for (const width of [390, 1280]) {
    const { ctx, page, errors } = await person(browser, "budi", width);
    await page.goto(APP + `/e/${SLUG}/judge?s=home`);
    await slow(page.locator('meta[name="hack-live"]')).toHaveAttribute("content", /judge-home/);
    await slow(page.getByRole("heading", { name: "Overview", level: 1 })).toBeVisible();
    if (width === 390) {
      // The walkthrough opens the first time only, and Skip records it on the server.
      await slow(page.getByRole("dialog", { name: "Thank you for judging" })).toBeVisible();
      await page.getByRole("button", { name: "Skip" }).click();
      await expect.poll(async () => (await call("GET", "/v1/hack/preferences", undefined, keys.budi)).body.judge_walkthrough_done).toBe(true);
    }
    await slow(page.getByRole("dialog")).toHaveCount(0);
    await slow(page.getByText("Your next step")).toBeVisible();
    await check(page, "judge-home", width);

    if (width === 1280) {
      // Rubric & brief, read and acknowledged.
      await page.goto(APP + `/e/${SLUG}/judge?s=rubric`);
      await slow(page.getByRole("heading", { name: "Rubric & brief", level: 1 })).toBeVisible();
      await slow(page.getByText("4 criteria · 20 points total")).toBeVisible();
      await check(page, "judge-rubric", width);
      await page.getByRole("button", { name: "I've read it" }).click();
      await slow(page.getByText("Done", { exact: true })).toBeVisible();

      // Teams I know: "I don't know any of these teams", said once.
      await page.goto(APP + `/e/${SLUG}/judge?s=queue&tab=conflicts`);
      await slow(page.getByRole("tab", { name: /Teams I know/ })).toHaveAttribute("data-state", "active");
      await page.getByRole("button", { name: "I don't know any of these teams" }).click();
      await slow(page.getByText("Saved: you don't know anyone on these teams.")).toBeVisible();

      // From Your events (a page loaded elsewhere) into Scoring: the page loads again at the judge address.
      await page.goto(APP + "/events");
      await page.getByRole("link", { name: /Real Night/ }).last().click();
      await slow(page.getByRole("heading", { name: "Overview", level: 1 })).toBeVisible();
      await slow(page.getByText("Scoring is open")).toBeVisible();
      await page.getByRole("link", { name: "Score next" }).click();
      await slow(page.getByRole("radiogroup", { name: "Idea" })).toBeVisible();
      slow(await page.evaluate(() => (performance.getEntriesByType("navigation")[0] as PerformanceNavigationTiming).name)).toMatch(/\/judge\?s=scoring/);
      // The scoring walkthrough opens the first time; Escape ends it.
      await slow(page.getByRole("dialog", { name: "Start with the card" })).toBeVisible();
      await page.keyboard.press("Escape");
      await slow(page.getByRole("dialog")).toHaveCount(0);
      await slow(page.getByRole("heading", { name: "Rice Ledger", level: 2 })).toBeVisible();
      await check(page, "judge-scoring", width);

      for (const [crit, n] of [["Idea", "4"], ["Execution", "3"], ["Design", "5"]] as const) await page.getByRole("radiogroup", { name: crit }).getByRole("radio", { name: n, exact: true }).click();
      // Save & finish with Demo unscored: one line, and a second press moves on.
      await page.getByRole("button", { name: "Save & finish" }).click();
      await slow(page.getByText("Demo has no score yet. Press again to move on.")).toBeVisible();
      await page.getByRole("radiogroup", { name: "Demo" }).getByRole("radio", { name: "2", exact: true }).click();
      await slow(page.getByText("Score saved")).toBeVisible();
      await page.getByLabel("Comment for the team").fill("Strong idea. Show the ledger on a phone.");
      await page.getByRole("button", { name: "Save & finish" }).click();
      await slow(page.getByRole("heading", { name: "My teams", level: 1 })).toBeVisible();
      await slow(page.getByRole("tab", { name: /Scored 1/ })).toHaveAttribute("data-state", "active");
      await slow(page.getByText("Scored 14/20")).toBeVisible();
      await check(page, "judge-queue", width);

      const team = ((await ok("GET", E + "/judge/queue", undefined, keys.budi)).queue as { team_id: string; done: boolean }[])[0];
      slow(team.done).toBe(true);
      const saved = await ok("GET", E + "/judge/scores/" + encodeURIComponent(team.team_id), undefined, keys.budi);
      slow((saved.scores as { points: number }[]).map((s) => s.points).sort()).toEqual([2, 3, 4, 5]);
      slow(saved.comment).toBe("Strong idea. Show the ledger on a phone.");
    }
    slow(errors).toEqual([]);
    await ctx.close();
  }
});

// Team, Entry and Website (the participant's team family): Ayu starts a team
// on an open event, sets it up, saves and submits an entry, and sends the
// team site's zip; then each screen is checked at a phone's width.
test("team, entry and website: start a team, fill the entry, send the site zip", async ({ browser }) => {
  test.setTimeout(300_000);
  // A loaded box answers slowly; these steps wait longer than the default 5 s.
  const slow = expect.configure({ timeout: 20_000 });
  const SL = "team-night";
  const E = "/v1/hack/events/" + SL;
  await signUp("ayu");
  await ok("POST", "/v1/hack/events", { slug: SL, title: "Team Night", organiser_name: "Orga", contact_email: "orga@example.com", purpose: "Testing", time_zone: "UTC", expected_participants: 20, starts_at: "2027-01-10", ends_at: "2027-01-11" }, keys.orga);
  await ok("POST", E + "/stage", { stage: "open" }, keys.orga);
  const ev = await ok("GET", E, undefined, keys.orga);
  const code = String((ev.organiser as Record<string, unknown>).join_url).split("/").pop()!;
  await ok("POST", "/v1/hack/join/" + code, { accept_coc: true, display_name: "Ayu" }, keys.ayu);

  const { ctx, page, errors } = await person(browser, "ayu", 1280);
  await page.goto(APP + `/e/${SL}/team?s=team`);
  await slow(page.locator('meta[name="hack-live"]')).toHaveAttribute("content", /participant-team/);
  // The walkthrough opens by itself the first time; Skip ends it.
  await slow(page.getByRole("dialog", { name: "Three ways onto a team" })).toBeVisible();
  await page.getByRole("button", { name: "Skip" }).click();
  await slow(page.getByRole("heading", { name: "How do you want to team up?" })).toBeVisible();
  await page.getByRole("radio", { name: "Start a new team" }).click();
  await page.getByRole("button", { name: "Start team" }).click();
  await slow(page.getByText("Enter a team name.")).toBeVisible();
  await page.getByLabel("Team name").fill("Kopi Kode");
  await page.getByRole("button", { name: "Start team" }).click();
  await slow(page.getByRole("heading", { name: "Kopi Kode", level: 1 })).toBeVisible();
  await slow(page.getByText("You started Kopi Kode. Copy its link to bring people in.")).toBeVisible();
  await slow(page.getByRole("button", { name: "Copy link" })).toBeVisible();
  await check(page, "team-members", 1280);

  // Settings: what the team is looking for, saved; locking with an Undo.
  await page.getByRole("tab", { name: "Settings" }).click();
  await page.getByLabel("Looking for").fill("A designer");
  await page.getByRole("button", { name: "Save", exact: true }).click();
  await slow(page.getByText("Saved").first()).toBeVisible();
  slow(((await ok("GET", E, undefined, keys.ayu)).me as { team: { looking_for: string } }).team.looking_for).toBe("A designer");
  await page.getByRole("button", { name: "Lock team", exact: true }).click();
  await slow(page.getByText("Kopi Kode is locked.", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Undo" }).click();
  await slow(page.getByText("Undone", { exact: true })).toBeVisible();
  await slow(page.getByRole("button", { name: "Lock team", exact: true })).toBeVisible();
  await slow.poll(async () => ((await ok("GET", E, undefined, keys.ayu)).me as { team: { locked: boolean } }).team.locked).toBe(false);
  await check(page, "team-settings", 1280);

  // Entry: a draft saved, then Submit names what is still missing.
  await page.goto(APP + `/e/${SL}/team?s=entry`);
  await slow(page.getByRole("heading", { name: "Entry", level: 1 })).toBeVisible();
  await page.getByLabel("Project name").fill("Kopi Map");
  await page.getByLabel("Problem, in one line").fill("Nobody knows which warung has coffee left.");
  await page.getByRole("button", { name: "Save draft" }).click();
  await slow(page.getByText("Saved").first()).toBeVisible();
  slow(((await ok("GET", E + "/entry", undefined, keys.ayu)).entry as { title: string }).title).toBe("Kopi Map");
  await slow(page.getByRole("article").getByRole("heading", { name: "Kopi Map" })).toBeVisible();
  await page.getByRole("button", { name: "Submit entry" }).first().click();
  const submitted = await call("GET", E + "/entry", undefined, keys.ayu);
  if (submitted.body.status !== "submitted") await slow(page.getByRole("alert").first()).toBeVisible();
  else await slow(page.getByText(/^Submitted/).first()).toBeVisible();
  await check(page, "entry", 1280);

  // Website: the guide, then a zip uploaded with the key and X-SH-CSRF.
  await page.goto(APP + `/e/${SL}/team?s=website`);
  await slow(page.getByRole("heading", { name: "Website", level: 1 })).toBeVisible();
  await slow(page.getByText("Not connected yet")).toBeVisible();
  await slow(page.getByRole("button", { name: "Copy prompt" })).toBeVisible();
  const zipDir = mkdtempSync(join(tmpdir(), "hk-zip-"));
  execFileSync("sh", ["-c", `cd ${zipDir} && printf '<!doctype html><title>Kopi</title><h1>Kopi Map</h1>' > index.html && zip -q site.zip index.html`]);
  await page.getByLabel("Your site archive").setInputFiles(join(zipDir, "site.zip"));
  await page.getByRole("button", { name: "Upload" }).click();
  // The upload reaches the site route signed in (a refused key would sign out); this
  // throwaway server has no certificate for team addresses, so it answers "not ready".
  await slow(page.getByText("Your team's address is still being set up. Try again in a few minutes.")).toBeVisible();
  rmSync(zipDir, { recursive: true, force: true });
  await check(page, "website", 1280);
  slow(errors).toEqual([]);
  await ctx.close();

  // The same three screens at a phone's width.
  const phone = await person(browser, "ayu", 390);
  for (const [s, h] of [["team", "Kopi Kode"], ["entry", "Entry"], ["website", "Website"]]) {
    await phone.page.goto(APP + `/e/${SL}/team?s=${s}`);
    await slow(phone.page.getByRole("heading", { name: h, level: 1 })).toBeVisible();
    await check(phone.page, s, 390);
  }
  slow(phone.errors).toEqual([]);
  await phone.ctx.close();
});

for (const width of [390, 1280]) {
  test(`the organiser runs the event: Home, Teams, Entries and Results, the new app @${width}`, async ({ browser }) => {
    test.setTimeout(300_000);
    const E = "/v1/hack/events/" + SLUG;
    // The walkthrough is seen already (it would open by itself the first time).
    await ok("PATCH", "/v1/hack/preferences", { organiser_walkthrough_done: true }, keys.orga);
    const { ctx, page, errors } = await person(browser, "orga", width);
    await page.goto(APP + `/e/${SLUG}/manage?s=overview`);
    await expect(page.locator('meta[name="hack-live"]')).toHaveAttribute("content", /organiser-overview/);
    await expect(page.getByRole("heading", { name: "Home", level: 1 })).toBeVisible();
    await check(page, "organiser-home", width);

    // Teams and Entries list Lumbung, with its submitted entry.
    await page.goto(APP + `/e/${SLUG}/manage?s=teams`);
    await expect(page.getByRole("heading", { name: "Teams", level: 1 })).toBeVisible();
    await expect(page.getByText("Lumbung").filter({ visible: true }).first()).toBeVisible();
    await check(page, "organiser-teams", width);
    await page.goto(APP + `/e/${SLUG}/manage?s=entries`);
    await expect(page.getByRole("heading", { name: "Entries", level: 1 })).toBeVisible();
    await expect(page.getByText("Submitted", { exact: true }).filter({ visible: true }).first()).toBeVisible();
    await check(page, "organiser-entries", width);

    if (width === 1280) {
      // Change stage to Results (the next step at Judging may be a checklist link), told to nobody.
      await page.goto(APP + `/e/${SLUG}/manage?s=overview`);
      await page.locator("[data-tour='change-stage']").click();
      await page.getByRole("dialog", { name: "Change stage" }).getByLabel("New stage").selectOption("results");
      await page.getByRole("dialog", { name: "Change stage" }).getByRole("button", { name: "Continue" }).click();
      await page.getByRole("dialog", { name: "Tell participants?" }).getByRole("button", { name: "Don’t post" }).click();
      await expect.poll(async () => ((await call("GET", E, undefined, keys.orga)).body.event as Record<string, unknown>).stage).toBe("results");
      await page.locator("[data-tour='next-move']").filter({ hasText: "Publish results" }).click();
      await page.getByRole("dialog").getByRole("button", { name: "Publish results" }).click();
      await page.getByRole("dialog", { name: "Tell participants?" }).getByRole("button", { name: "Post", exact: true }).click();
      await expect.poll(async () => JSON.stringify((await call("GET", E + "/announcements", undefined, keys.orga)).body)).toContain("Results are out");
      await page.goto(APP + `/e/${SLUG}/manage?s=results`);
      await expect(page.getByText(/^Published/).first()).toBeVisible();
      await check(page, "organiser-results", width);
    }
    expect(errors).toEqual([]);
    await ctx.close();
  });
}



for (const width of [390, 1280]) {
  test(`sign in with a password, back to where the person was going @${width}`, async ({ browser }) => {
    const { ctx, page, errors } = await person(browser, "", width);
    // Signed out on an event screen: sent to the new sign-in page with next.
    await page.goto(APP + `/e/${SLUG}/team?s=submissions`);
    await page.waitForURL(/\/signin\?next=/);
    await expect(page.locator("#root")).toHaveCount(1);
    await expect(page.getByRole("heading", { name: "Sign in", level: 1 })).toBeVisible();
    await check(page, "signin", width);

    // A wrong password is said in plain words, and nothing is stored.
    await page.getByLabel("Email or username").fill("siti");
    await page.getByLabel("Password", { exact: true }).fill("not-the-password");
    await page.getByRole("button", { name: "Sign in", exact: true }).click();
    await expect(page.getByText("Wrong username or password.")).toBeVisible();
    expect(await page.evaluate(() => localStorage.getItem("apiKey"))).toBeNull();

    // The right one: the key is kept in this browser, the SSO cookie is set, and the page goes on to next.
    await page.getByLabel("Password", { exact: true }).fill("correct-horse-siti");
    await page.getByRole("button", { name: "Sign in", exact: true }).click();
    await page.waitForURL(new RegExp(`/e/${SLUG}/team\\?s=submissions$`));
    await expect(page.getByRole("heading", { name: "Submissions", level: 1 })).toBeVisible();
    expect(await page.evaluate(() => localStorage.getItem("apiKey"))).toMatch(/.{20,}/);
    const cookies = await ctx.cookies(APP);
    expect(cookies.some((c) => c.name.includes("sso")), "SSO cookie set: " + cookies.map((c) => c.name).join(",")).toBe(true);

    // Signed in already: /signin goes straight on, to Siti's one current event.
    await page.goto(APP + "/signin");
    await page.waitForURL(new RegExp(`/e/${SLUG}/team`));
    expect(errors).toEqual([]);
    await ctx.close();
  });
}

test("joining from a link, then the participant's Schedule and Account with the leave guard", async ({ browser }) => {
  // A fresh event that is open for sign-up, and a new person with no name yet.
  await ok("POST", "/v1/hack/events", { slug: "join-night", title: "Join Night", organiser_name: "Orga", contact_email: "orga@example.com", purpose: "Testing", time_zone: "UTC", expected_participants: 20, starts_at: "2027-02-10", ends_at: "2027-02-11" }, keys.orga);
  await ok("POST", "/v1/hack/events/join-night/stage", { stage: "open" }, keys.orga);
  const ev = await ok("GET", "/v1/hack/events/join-night", undefined, keys.orga);
  const code = String((ev.organiser as Record<string, unknown>).join_url).split("/").pop()!;
  await signUp("joiner");
  const { ctx, page, errors } = await person(browser, "joiner", 390);

  await page.goto(APP + "/join/" + code);
  await expect(page.locator("#root")).toHaveCount(1);
  await expect(page.getByRole("heading", { name: /Join Night/, level: 1 })).toBeVisible();
  await check(page, "join", 390);
  await page.getByRole("button", { name: "Join event" }).click();
  await expect(page.getByText("Enter your name.").first()).toBeVisible();
  await page.getByLabel("Your name").fill("Joni");
  await page.getByRole("checkbox", { name: "I agree to the code of conduct" }).check();
  await page.getByRole("button", { name: "Join event" }).click();
  await page.waitForURL(/\/e\/join-night\/team/);
  expect((await call("GET", "/v1/hack/events/join-night", undefined, keys.joiner)).status).toBe(200);

  // The participant's Schedule is the new app.
  await page.goto(APP + "/e/join-night/team?s=schedule");
  await expect(page.locator("#root")).toHaveCount(1);
  await expect(page.getByRole("heading", { name: "Schedule", level: 1 })).toBeVisible();
  await check(page, "schedule", 390);

  // Account: the name is the one given when joining; an unsaved edit asks before leaving.
  await page.goto(APP + "/account");
  await expect(page.getByRole("heading", { name: "Account", level: 1 })).toBeVisible();
  await expect(page.locator("#acct-name")).toHaveValue("Joni");
  await check(page, "account", 390);
  await page.locator("#acct-name").fill("Joni Mitchell");
  await page.getByRole("link", { name: "Your events" }).first().click();
  await expect(page.getByRole("dialog", { name: "Leave without saving?" })).toBeVisible();
  await page.getByRole("button", { name: "Stay" }).click();
  await page.getByRole("button", { name: "Save" }).click();
  await expect(page.getByText("Saved").first()).toBeVisible();
  expect((await call("GET", "/v1/me", undefined, keys.joiner)).body.display_name).toBe("Joni Mitchell");
  await page.getByRole("link", { name: "Your events" }).first().click();
  await expect(page.getByRole("heading", { name: "Your events", level: 1 })).toBeVisible();
  expect(errors).toEqual([]);
  await ctx.close();
});
