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
import { expect, test, type BrowserContext, type Page } from "@playwright/test";
import { axe, layout } from "../../kit/tests/checks";

test.describe.configure({ mode: "serial" });

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
  test.setTimeout(240_000);
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
  for (let i = 0; i < 200; i++) {
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

    // A screen not rebuilt yet is the old app's: a full page load, which the server answers with app.html.
    await page.goto(APP + `/e/${SLUG}/team?s=schedule`);
    await expect(page.locator('script[src="/hack/app.js"]')).toHaveCount(1);
    await expect(page.locator("main h1")).toBeVisible();

    // From the old app, Submissions is reached through the server again: the new app.
    await page.goto(APP + `/e/${SLUG}/team?s=submissions`);
    await expect(page.locator("#root")).toHaveCount(1);
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
