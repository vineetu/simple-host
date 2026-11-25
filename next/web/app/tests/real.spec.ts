// The real service, clicked through at 390 and 1280 px: the Go binary on a
// throwaway database (in the test Postgres the Go tests use, 127.0.0.1:54329),
// a fake mail service that keeps the sign-in codes, and the browser's https
// requests for *.sh.test and *.sh.site answered by the binary.
// Pictures land in test-results/real/ to be looked at; they are not baselines.
// Redirects answered by the router make Chromium log a failed lookup for the
// next address before it is routed too, so after one the test waits for what
// the page shows, not for its address.
import { execFileSync, spawn, type ChildProcess } from "node:child_process";
import { copyFileSync, mkdirSync, mkdtempSync, openSync, readFileSync, rmSync } from "node:fs";
import http from "node:http";
import net from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { expect, test, type BrowserContext, type Page } from "@playwright/test";
import { axe, layout } from "./checks";

test.describe.configure({ mode: "serial" });

const NEXT = join(import.meta.dirname, "../../..");
const PG = "sh-next-test";
const DB = "fe_" + Date.now().toString(36);
let PORT = 0; // the service; the fake mail service is on a second free port
let MAILPORT = 0;
const APP = "https://sh.test";
const site = (name: string, owner: string) => `https://${name}--${owner}.sh.site`;

let server: ChildProcess;
let mail: http.Server;
let dir: string;
const codes: Record<string, string> = {};
let anaCookie = "";

function psql(sql: string, db = "postgres") {
  return execFileSync("docker", ["exec", PG, "psql", "-U", "postgres", "-d", db, "-tAc", sql], { encoding: "utf8" });
}

/** call is one request straight to the binary, as a script would make it. */
function call(method: string, host: string, path: string, body?: unknown, cookie = ""): Promise<{ status: number; body: unknown; cookie: string }> {
  return new Promise((resolve, reject) => {
    const data = body === undefined ? undefined : JSON.stringify(body);
    const req = http.request(
      {
        host: "127.0.0.1",
        port: PORT,
        method,
        path,
        headers: { host, "x-forwarded-proto": "https", "x-sh-csrf": "1", "content-type": "application/json", cookie },
      },
      (res) => {
        let b = "";
        res.on("data", (c) => (b += c));
        res.on("end", () => {
          const set = (res.headers["set-cookie"] || []).map((c) => c.split(";")[0]).join("; ");
          resolve({ status: res.statusCode!, body: b && b.startsWith("{") ? JSON.parse(b) : b, cookie: set || cookie });
        });
      },
    );
    req.on("error", reject);
    req.end(data);
  });
}

async function code(email: string) {
  for (let i = 0; i < 50 && !codes[email]; i++) await new Promise((r) => setTimeout(r, 100));
  const c = codes[email];
  delete codes[email];
  return c;
}

/** signInByScript signs a person in without a browser and claims their handle. */
async function signInByScript(email: string, handle: string) {
  await call("POST", "sh.test", "/v1/auth/code", { email });
  const r = await call("POST", "sh.test", "/v1/auth/verify", { email, code: await code(email), handle });
  expect(r.status).toBe(200);
  return r.cookie;
}

async function route(ctx: BrowserContext) {
  await ctx.route(/^https:\/\/[^/]*\.?sh\.(test|site)(\/|$)/, async (r) => {
    const req = r.request();
    const u = new URL(req.url());
    if (process.env.SH_DEBUG) console.log("route", req.method(), u.href);
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
      p.on("error", (e) => (console.log("route error", u.href, String(e)), reject(e)));
      p.end(req.postDataBuffer() ?? undefined);
    });
    await r.fulfill(res).catch((e) => console.log("route fulfill", u.href, String(e)));
  });
}

function freePort(): Promise<number> {
  return new Promise((resolve) => {
    const s = net.createServer().listen(0, "127.0.0.1", () => {
      const p = (s.address() as net.AddressInfo).port;
      s.close(() => resolve(p));
    });
  });
}

test.beforeAll(async () => {
  test.setTimeout(180_000);
  PORT = await freePort();
  MAILPORT = await freePort();
  dir = mkdtempSync(join(tmpdir(), "sh-fe-"));
  execFileSync("go", ["build", "-o", join(dir, "simple-host-next"), "./cmd/simple-host-next"], { cwd: NEXT, stdio: "inherit" });
  psql(`CREATE DATABASE ${DB}`);
  mail = http
    .createServer((req, res) => {
      let b = "";
      req.on("data", (c) => (b += c));
      req.on("end", () => {
        const m = JSON.parse(b);
        const c = /code is (\d{6})/.exec(m.text)?.[1];
        if (c) codes[m.to[0]] = c;
        res.setHeader("content-type", "application/json");
        res.end('{"id":"fake"}');
      });
    })
    .listen(MAILPORT, "127.0.0.1");
  server = spawn(join(dir, "simple-host-next"), [], {
    env: {
      ...process.env,
      DATABASE_URL: `postgres://postgres:test@127.0.0.1:54329/${DB}?sslmode=disable`,
      SH_ADDR: `127.0.0.1:${PORT}`,
      SH_DATA_DIR: join(dir, "data"),
      SH_APP_DOMAIN: "sh.test",
      SH_SITE_DOMAIN: "sh.site",
      PASSCODE_ENC_KEY: Buffer.alloc(32, 7).toString("base64"),
      RESEND_API_KEY: "re_fake",
      SH_MAIL_URL: `http://127.0.0.1:${MAILPORT}/emails`,
      GOOGLE_CLIENT_ID: "fake.apps.googleusercontent.com",
      GOOGLE_CLIENT_SECRET: "fake",
    },
    stdio: ["ignore", openSync(join(dir, "server.log"), "w"), openSync(join(dir, "server.log"), "a")],
  });
  for (let i = 0; i < 100; i++) {
    try {
      if ((await call("GET", "sh.test", "/signin")).status === 200) break;
    } catch {
      /* not up yet */
    }
    await new Promise((r) => setTimeout(r, 100));
  }
  if (server.exitCode !== null) throw new Error("the service stopped: " + readFileSync(join(dir, "server.log"), "utf8"));
  // People and sites: ops is the operator; ana has an open, a passcode, a
  // named-people, an offline and a taken-down site.
  await signInByScript("ops@example.com", "ops");
  psql(`UPDATE people SET operator = true WHERE email = 'ops@example.com'`, DB);
  const ana = (anaCookie = await signInByScript("ana@example.com", "ana"));
  for (const name of ["recipes", "bakery", "club", "zine", "gone"])
    expect((await call("PUT", "sh.test", `/v1/sites/${name}`, { files: { "index.html": `<h1>${name}</h1>` } }, ana)).status).toBe(200);
  await call("PATCH", "sh.test", "/v1/sites/bakery", { access: "passcode", passcode: "crumb42x" }, ana);
  await call("PATCH", "sh.test", "/v1/sites/club", { access: "viewers", viewers: ["ben@example.com"] }, ana);
  await call("PATCH", "sh.test", "/v1/sites/zine", { offline: true }, ana);
  await signInByScript("sam@example.com", "sam");
});

test.afterAll(async () => {
  server?.kill();
  if (dir) {
    mkdirSync("test-results/real", { recursive: true });
    copyFileSync(join(dir, "server.log"), "test-results/real/server.log");
  }
  mail?.close();
  try {
    psql(`DROP DATABASE IF EXISTS ${DB} WITH (FORCE)`);
  } catch {
    /* already gone */
  }
  if (dir) rmSync(dir, { recursive: true, force: true });
});

async function check(page: Page, name: string, width: number) {
  const problems = [...(await layout(page)), ...(await axe(page))];
  await page.screenshot({ path: `test-results/real/${name}-${width}.png`, fullPage: true });
  expect(problems, name).toEqual([]);
}

test("the installable app's files are served", async () => {
  for (const [path, type] of [
    ["/manifest.webmanifest", "application/manifest+json"],
    ["/sw.js", "javascript"],
    ["/pages.css", "text/css"],
    ["/app/icon-192.png", "image/png"],
  ]) {
    const r = await new Promise<http.IncomingMessage>((ok) => http.get({ host: "127.0.0.1", port: PORT, path, headers: { host: "sh.test" } }, ok));
    r.resume();
    expect(r.statusCode, path).toBe(200);
    expect(r.headers["content-type"], path).toContain(type);
  }
});

for (const width of [390, 1280]) {
  test(`sign in and run the admin page @${width}`, async ({ browser }) => {
    test.setTimeout(90_000);
    const ctx = await browser.newContext({ viewport: { width, height: 860 }, serviceWorkers: "block" });
    await route(ctx);
    const page = await ctx.newPage();
    const errors: string[] = [];
    page.on("pageerror", (e) => errors.push(e.message));

    // Signed out, /admin sends to sign-in and back.
    await page.goto(APP + "/admin");
    await page.waitForURL(/\/signin\?next=%2Fadmin|\/signin\?next=\/admin/);
    await expect(page.getByRole("link", { name: "Continue with Google", exact: true })).toBeVisible();
    await check(page, "signin-email", width);
    await page.getByLabel("Email").fill("ops@example");
    await page.getByRole("button", { name: "Continue", exact: true }).click();
    await expect(page.getByRole("alert")).toHaveText("That email address does not look right.");
    await page.getByLabel("Email").fill("ops@example.com");
    await page.getByRole("button", { name: "Continue", exact: true }).click();
    await expect(page.getByRole("heading", { name: "Check your email" })).toBeVisible();
    await page.getByLabel("Code").fill("000000");
    await page.getByRole("button", { name: "Sign in", exact: true }).click();
    await expect(page.getByRole("alert")).toContainText("wrong or has run out");
    await check(page, "signin-badcode", width);
    await page.getByLabel("Code").fill(await code("ops@example.com"));
    await page.getByRole("button", { name: "Sign in", exact: true }).click();

    // Admin: overview, users, sites, API, moderation.
    await page.waitForURL(APP + "/admin");
    await expect(page.getByText("Live sites of 5", { exact: true })).toBeVisible();
    await check(page, "admin-overview", width);
    await page.getByRole("tab", { name: "Users", exact: true }).click();
    await expect(page).toHaveURL(/#users$/);
    await page.getByRole("searchbox").fill("ana");
    await expect(page.getByText(/^\(1 of \d\)$/)).toBeVisible();
    await page.getByRole("searchbox").fill("");
    await page.getByRole("combobox", { name: "Sort" }).selectOption({ label: "Handle: A to Z" });
    await check(page, "admin-users", width);
    await page.getByRole("searchbox").fill("sam");
    await page.getByRole("button", { name: "Show details", exact: true }).filter({ visible: true }).first().click();
    await page.getByRole("button", { name: "Suspend", exact: true }).click();
    await page.getByLabel("Reason, in one line").fill("Testing the suspend button");
    await check(page, "admin-suspend-dialog", width);
    await page.getByRole("dialog").getByRole("button", { name: "Suspend", exact: true }).click();
    await expect(page.getByText("Suspended sam.", { exact: true })).toBeVisible();
    await page.getByRole("searchbox").fill("");

    await page.getByRole("tab", { name: "Sites", exact: true }).click();
    await page.getByRole("button", { name: "Taken down", exact: true }).click();
    await page.getByRole("button", { name: "All", exact: true }).click();
    await page.getByRole("button", { name: "Actions for gone", exact: true }).filter({ visible: true }).click();
    await page.getByRole("menuitem", { name: "Take down", exact: true }).click();
    await page.getByLabel("Reason, in one line").fill("Breaks the rules");
    await page.getByRole("dialog").getByRole("button", { name: "Take down", exact: true }).click();
    await expect(page.getByText("Took down gone.", { exact: true })).toBeVisible();
    await check(page, "admin-sites", width);

    await page.getByRole("tab", { name: "API", exact: true }).click();
    await expect(page.getByRole("heading", { name: "Where API calls come from" })).toBeVisible();
    await page.getByRole("button", { name: "7 days", exact: true }).click();
    await expect(page.locator("[aria-busy=true]")).toHaveCount(0);
    await check(page, "admin-api", width);

    await page.getByRole("tab", { name: "Moderation", exact: true }).click();
    await expect(page.getByText("Breaks the rules", { exact: true }).filter({ visible: true }).first()).toBeVisible();
    await check(page, "admin-moderation", width);
    // Put things back for the other width.
    const back = page.getByRole("button", { name: /^(Restore|Re-enable)$/ }).filter({ visible: true });
    for (let n = await back.count(); n > 0; n--) {
      await back.first().click();
      await expect(back).toHaveCount(n - 1);
    }
    expect(errors).toEqual([]);
    await ctx.close();
  });

  test(`gate pages and the visitor's sign-in @${width}`, async ({ browser }) => {
    test.setTimeout(60_000);
    const ctx = await browser.newContext({ viewport: { width, height: 860 }, serviceWorkers: "block" });
    await route(ctx);
    const page = await ctx.newPage();
    if (process.env.SH_DEBUG) page.on("requestfailed", (r) => console.log("failed", r.url(), r.failure()?.errorText));
    const noScript = async () => expect(await page.locator("script").count()).toBe(0);

    await page.goto(site("bakery", "ana"));
    await expect(page.getByRole("heading", { name: "Enter the passcode" })).toBeVisible();
    await noScript();
    await check(page, "gate-passcode", width);
    await page.getByLabel("Passcode").fill("wrong");
    await page.getByRole("button", { name: "Open site", exact: true }).click();
    await expect(page.getByRole("alert")).toContainText("not right");
    await check(page, "gate-passcode-wrong", width);

    for (const [name, title] of [
      ["zine", "This site is offline"],
      ["nothing-here", "That site isn’t here"],
    ]) {
      await page.goto(site(name, "ana"));
      await expect(page.getByRole("heading", { name: title })).toBeVisible();
      await noScript();
      await check(page, "gate-" + name, width);
    }

    // The text pages, on the shared stylesheet.
    for (const [path, title] of [
      ["/privacy", "Privacy"],
      ["/terms", "Terms"],
      ["/support", "Support"],
      ["/docs", "API reference"],
    ]) {
      await page.goto(APP + path);
      await expect(page.getByRole("heading", { level: 1, name: title })).toBeVisible();
      await check(page, "text" + path.replace("/", "-"), width);
    }

    await page.goto(site("club", "ana"));
    await expect(page.getByRole("heading", { name: "This site is private" })).toBeVisible();
    await noScript();
    await check(page, "gate-private", width);

    // Sign in from the site: the hop to the platform, then Ask for access.
    await page.getByRole("link", { name: "Sign in", exact: true }).click();
    await expect(page.getByText("to continue to the site. No password.", { exact: true })).toBeVisible();
    await check(page, "signin-hop", width);
    await page.getByLabel("Email").fill(`cy${width}@example.com`);
    await page.getByRole("button", { name: "Continue", exact: true }).click();
    await page.getByLabel("Code").fill(await code(`cy${width}@example.com`));
    await page.getByRole("button", { name: "Sign in", exact: true }).click();
    await expect(page.getByRole("heading", { name: "You do not have access" })).toBeVisible();
    await noScript();
    await check(page, "gate-ask", width);
    await page.getByRole("button", { name: "Ask for access", exact: true }).click();
    await expect(page.getByRole("status")).toContainText("We told the owner");
    await check(page, "gate-asked", width);
    // Signing out of the site (a plain form) goes back to the private page.
    await page.getByRole("button", { name: "Sign out", exact: true }).click();
    await expect(page.getByRole("heading", { name: "This site is private" })).toBeVisible();

    // Signed in on the platform already: a second site only asks to continue.
    // (a redirect answered by the test's router: wait for the first answer only)
    await page.goto(site("recipes", "ana") + "/_sh/signin?return=/", { waitUntil: "commit" }).catch(() => {});
    await expect(page.getByRole("heading", { name: "Continue as" })).toBeVisible();
    await check(page, "signin-continue", width);
    await page.getByRole("button", { name: "Continue", exact: true }).click();
    await expect(page.getByRole("heading", { name: "recipes" })).toBeVisible();
    await ctx.close();
  });
}

// A tiny real PNG (1 x 1), so the photo tools shrink it as they would a photo.
const PNG = Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==", "base64");

for (const width of [390, 1280]) {
  test(`an upload link: add a photo @${width}`, async ({ browser }) => {
    test.setTimeout(60_000);
    const made = await call("POST", "sh.test", "/v1/sites/recipes/upload-links", { kind: "files", namespace: "photos" }, anaCookie);
    expect(made.status).toBe(200);
    const url = (made.body as { url: string }).url;
    expect(url).toMatch(/^https:\/\/sh\.test\/upload\/up_/);
    const ctx = await browser.newContext({ viewport: { width, height: 860 }, serviceWorkers: "block" });
    await route(ctx);
    const page = await ctx.newPage();
    const errors: string[] = [];
    page.on("pageerror", (e) => errors.push(e.message));
    await page.goto(url);
    await expect(page.getByRole("heading", { name: "Add photos and files" })).toBeVisible();
    await check(page, "upload-files", width);
    await page.locator("input[type=file]").setInputFiles({ name: "Kitchen Table.png", mimeType: "image/png", buffer: PNG });
    await expect(page.getByLabel("Name")).toHaveValue("kitchen-table");
    await expect(page.getByText("1 file ready", { exact: false })).toBeVisible();
    await page.getByLabel("Name").fill("kitchen");
    await check(page, "upload-chosen", width);
    await page.getByRole("button", { name: "Save to site", exact: true }).click();
    await expect(page.getByRole("heading", { name: "Saved to the site" })).toBeVisible();
    await expect(page.getByText(/^I added kitchen\.(webp|jpg) to the site's photos files\.$/)).toBeVisible();
    await check(page, "upload-done", width);
    // The link works once.
    await page.reload();
    await expect(page.getByRole("heading", { name: "This link does not work" })).toBeVisible();
    await expect(page.getByText("This upload link was used or has run out. Ask for a new one.")).toBeVisible();
    expect(errors).toEqual([]);
    await ctx.close();
  });
}
