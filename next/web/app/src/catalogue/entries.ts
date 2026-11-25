// Every screen in every state. Each entry is a URL inside the owner app, the
// API's answers for it, and optional steps the catalogue performs once the
// screen is up (to open a dialog, reach the code step, and so on).
// The visual checks open each entry at 390 and 1280 px.
import { ApiError } from "@/lib/api";
import { admin } from "./fixtures";
import { dashboardEntries } from "./dashboard";

/** An answer: a value, a refusal, or "pending" (the request never ends). */
export type Answer = unknown | ApiError | "pending";
export type Route = (method: string, path: string, body: unknown) => Answer;
// ---- Upload: { files } picks made-up files into a file input (name, type, size in bytes).
export type FakeFile = { name: string; type: string; size: number };
export type Step = { click: string } | { fill: [string, string] } | { select: [string, string] } | { files: [string, FakeFile[]] };

export type Entry = {
  id: string;
  title: string;
  url: string;
  api: Route;
  steps?: Step[];
  google?: boolean;
  // ---- Upload: the service's file-store limit, smaller to show "too big" without a huge file.
  filesLimit?: number;
};

const refuse = (status: number, code: string, fix: string) => new ApiError(status, code, fix);

function adminApi(state: Parameters<typeof admin>[0], o: { load?: Answer; me?: Answer } = {}): Route {
  const a = admin(state);
  return (method, path) => {
    if (o.load !== undefined && path.startsWith("/v1/admin/")) return o.load;
    if (path === "/v1/me") return o.me ?? { id: "p-admin", email: "admin@example.com", handle: "admin", operator: true };
    if (path.startsWith("/v1/admin/overview?range=")) return a.growth(new URLSearchParams(path.split("?")[1]).get("range") || "30d");
    if (path === "/v1/admin/overview") return a.overview;
    if (path === "/v1/admin/people" && method === "GET") return a.people;
    if (path === "/v1/admin/sites") return a.sites;
    return {};
  };
}

const signinApi =
  (o: { code?: Answer; verify?: Answer; hop?: Answer } = {}): Route =>
  (_m, path, body) => {
    const b = (body || {}) as Record<string, unknown>;
    if (path === "/v1/auth/code") return o.code ?? { sent: true };
    if (path === "/v1/auth/verify") {
      if (b.hop && !b.code && !b.confirm) return o.hop ?? refuse(400, "no_code", "Send your email and the code we emailed you.");
      return o.verify ?? "pending";
    }
    return {};
  };

// ---- Upload
const uploadApi =
  (o: { check?: Answer; send?: Answer } = {}): Route =>
  (method, path) => {
    if (path === "/v1/uploads/tok" && method === "GET") return o.check ?? { kind: "files", namespace: "photos" };
    if (path === "/v1/uploads/tok" && method === "PUT") return o.send ?? { namespace: "photos", files: ["img-2041.webp", "front-door.webp", "menu-spring.pdf"] };
    return {};
  };
const pickThree: Step = {
  files: [
    "input[type=file]",
    [
      { name: "IMG_2041.HEIC", type: "image/heic", size: 3_240_000 },
      { name: "Front door.jpg", type: "image/jpeg", size: 2_610_000 },
      { name: "Menu (spring).pdf", type: "application/pdf", size: 184_000 },
    ],
  ],
};
// ---- end Upload

const toCode: Step[] = [{ fill: ["#email", "ana@example.com"] }, { click: "text=Continue" }];

export const entries: Entry[] = [
  // ---- Sign-in
  { id: "signin-email", title: "Sign in: email step, with Google", url: "/signin", api: signinApi(), google: true },
  { id: "signin-email-nogoogle", title: "Sign in: email step, no Google on this install", url: "/signin", api: signinApi() },
  { id: "signin-hop", title: "Sign in: a visitor from a site", url: "/signin?hop=tok", api: signinApi(), google: true },
  {
    id: "signin-bademail",
    title: "Sign in: email that does not look right",
    url: "/signin",
    api: signinApi(),
    google: true,
    steps: [{ fill: ["#email", "ana@example"] }, { click: "text=Continue" }],
  },
  {
    id: "signin-sendfail",
    title: "Sign in: the code could not be sent",
    url: "/signin",
    api: signinApi({ code: refuse(429, "slow_down", "Too many codes asked for. Wait a minute and try again.") }),
    google: true,
    steps: toCode,
  },
  { id: "signin-code", title: "Sign in: check your email", url: "/signin", api: signinApi(), google: true, steps: toCode },
  {
    id: "signin-badcode",
    title: "Sign in: wrong code",
    url: "/signin",
    api: signinApi({ verify: refuse(401, "bad_code", "This code is wrong or has run out. Ask for a new one.") }),
    google: true,
    steps: [...toCode, { fill: ["#code", "123456"] }, { click: "text=Sign in" }],
  },
  {
    id: "signin-continue",
    title: "Sign in: Continue as <email> to <site>?",
    url: "/signin?hop=tok",
    api: signinApi({ hop: { email: "ana@example.com", confirm: "recipes.ana.simple-host.app" } }),
  },
  {
    id: "signin-continue-error",
    title: "Sign in: the site's link ran out",
    url: "/signin?hop=tok",
    api: (m, p, b) =>
      (b as { confirm?: boolean })?.confirm
        ? refuse(400, "bad_hop", "This sign-in link has run out. Go back to the site and sign in again.")
        : signinApi({ hop: { email: "ana@example.com", confirm: "recipes.ana.simple-host.app" } })(m, p, b),
    steps: [{ click: "text=Continue" }],
  },

  // ---- Admin
  { id: "admin-loading", title: "Admin: loading", url: "/admin", api: adminApi("full", { load: "pending" }) },
  { id: "admin-error", title: "Admin: could not load", url: "/admin", api: adminApi("full", { load: refuse(500, "server_error", "Something went wrong on our side. Try again in a minute.") }) },
  { id: "admin-not-operator", title: "Admin: signed in, not the operator", url: "/admin", api: adminApi("full", { load: refuse(403, "operator_only", "Only the operator can see this."), me: { id: "p-ana", email: "ana@example.com", handle: "ana", operator: false } }) },
  { id: "admin-overview", title: "Admin: overview", url: "/admin", api: adminApi("full") },
  { id: "admin-overview-empty", title: "Admin: overview of a new install", url: "/admin", api: adminApi("empty") },
  { id: "admin-overview-diskfull", title: "Admin: overview, the disk nearly full", url: "/admin", api: adminApi("nearlyfull") },
  { id: "admin-users", title: "Admin: users", url: "/admin#users", api: adminApi("full") },
  { id: "admin-users-open", title: "Admin: a user's details", url: "/admin#users", api: adminApi("full"), steps: [{ click: "[aria-label='Show details']@4" }] },
  {
    id: "admin-users-suspend",
    title: "Admin: suspend dialog",
    url: "/admin#users",
    api: adminApi("full"),
    steps: [{ click: "[aria-label='Show details']" }, { click: "text=Suspend" }, { fill: ["#ask-answer", "Sends spam"] }],
  },
  { id: "admin-users-filtered", title: "Admin: users, suspended only", url: "/admin#users", api: adminApi("full"), steps: [{ click: "text=Suspended" }] },
  { id: "admin-users-nomatch", title: "Admin: users, nothing matches", url: "/admin#users", api: adminApi("full"), steps: [{ fill: ["input[type=search]", "zzz"] }] },
  { id: "admin-sites", title: "Admin: sites", url: "/admin#sites", api: adminApi("full") },
  { id: "admin-sites-sorted", title: "Admin: sites by size", url: "/admin#sites", api: adminApi("full"), steps: [{ select: ["select", "size:desc"] }] },
  { id: "admin-sites-filtered", title: "Admin: sites with their own address", url: "/admin#sites", api: adminApi("full"), steps: [{ click: "text=Has own address" }] },
  { id: "admin-sites-menu", title: "Admin: a site's actions", url: "/admin#sites", api: adminApi("full"), steps: [{ click: "[aria-label='Actions for recipes']" }] },
  {
    id: "admin-sites-takedown",
    title: "Admin: take down dialog",
    url: "/admin#sites",
    api: adminApi("full"),
    steps: [{ click: "[aria-label='Actions for recipes']" }, { click: "text=Take down" }],
  },
  { id: "admin-sites-paged", title: "Admin: 120 sites, second page", url: "/admin#sites", api: adminApi("many"), steps: [{ click: "text=Next" }] },
  { id: "admin-api", title: "Admin: API growth and the world map", url: "/admin#api", api: adminApi("full") },
  { id: "admin-api-empty", title: "Admin: API growth with no calls yet", url: "/admin#api", api: adminApi("empty") },
  { id: "admin-moderation", title: "Admin: moderation", url: "/admin#moderation", api: adminApi("full") },
  { id: "admin-moderation-empty", title: "Admin: nothing to moderate", url: "/admin#moderation", api: adminApi("empty") },
  {
    id: "admin-moderation-undo",
    title: "Admin: restored, with Undo",
    url: "/admin#moderation",
    api: adminApi("full"),
    steps: [{ click: "text=Restore" }],
  },
  { id: "admin-tools", title: "Admin: make an account (self-hosted)", url: "/admin#tools", api: adminApi("selfhosted") },
  {
    id: "admin-tools-bad",
    title: "Admin: make an account, email not right",
    url: "/admin#tools",
    api: adminApi("selfhosted"),
    steps: [{ fill: ["#make-email", "someone@"] }, { click: "text=Make account" }],
  },

  // ---- Dashboard (catalogue/dashboard.ts)
  ...dashboardEntries,
  // ---- Upload
  { id: "upload-loading", title: "Upload link: checking the link", url: "/upload/tok", api: uploadApi({ check: "pending" }) },
  { id: "upload-files", title: "Upload link: photos and files, nothing chosen", url: "/upload/tok", api: uploadApi() },
  { id: "upload-chosen", title: "Upload link: three files chosen, ready to save", url: "/upload/tok", api: uploadApi(), steps: [pickThree] },
  {
    id: "upload-sending",
    title: "Upload link: sending, part way",
    url: "/upload/tok",
    api: uploadApi({ send: { progress: 0.62 } }),
    steps: [pickThree, { click: "text=Save to site" }],
  },
  { id: "upload-done", title: "Upload link: saved, with the message for the chat", url: "/upload/tok", api: uploadApi(), steps: [pickThree, { click: "text=Save to site" }] },
  {
    id: "upload-sendfail",
    title: "Upload link: the upload stopped",
    url: "/upload/tok",
    api: uploadApi({ send: refuse(0, "offline", "The upload stopped: Simple Host could not be reached. Check your connection and try again.") }),
    steps: [pickThree, { click: "text=Save to site" }],
  },
  {
    id: "upload-toomany",
    title: "Upload link: more than 30 at once",
    url: "/upload/tok",
    api: uploadApi(),
    steps: [{ files: ["input[type=file]", Array.from({ length: 31 }, (_, i) => ({ name: `IMG_${3000 + i}.jpg`, type: "image/jpeg", size: 240_000 }))] }],
  },
  {
    id: "upload-toobig",
    title: "Upload link: a file too big for the site, left out",
    url: "/upload/tok",
    api: uploadApi(),
    filesLimit: 5_000_000,
    steps: [{ files: ["input[type=file]", [{ name: "kitchen.jpg", type: "image/jpeg", size: 3_100_000 }, { name: "tour-video.mp4", type: "video/mp4", size: 6_200_000 }]] }],
  },
  {
    id: "upload-used",
    title: "Upload link: used already",
    url: "/upload/tok",
    api: uploadApi({ check: refuse(410, "link_used", "This upload link has been used or has run out. Ask your chat for a new one.") }),
  },
  { id: "upload-archive", title: "Upload link: publish a whole site", url: "/upload/tok", api: uploadApi({ check: { kind: "archive" } }) },
  {
    id: "upload-archive-chosen",
    title: "Upload link: the site's archive chosen",
    url: "/upload/tok",
    api: uploadApi({ check: { kind: "archive" } }),
    steps: [{ files: ["input[type=file]", [{ name: "bakery-site.zip", type: "application/zip", size: 1_840_000 }]] }],
  },
  {
    id: "upload-archive-done",
    title: "Upload link: the site published",
    url: "/upload/tok",
    api: uploadApi({ check: { kind: "archive" }, send: { version: 4, url: "https://bakery.ana.simple-host.app/" } }),
    steps: [{ files: ["input[type=file]", [{ name: "bakery-site.zip", type: "application/zip", size: 1_840_000 }]] }, { click: "text=Publish" }],
  },
];
