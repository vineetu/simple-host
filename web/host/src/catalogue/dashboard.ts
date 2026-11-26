// The dashboard's catalogue entries and their fixtures: ana's account with
// five sites and a team's, in every state the pages show.
import { ApiError } from "@/lib/api";
import type { Me, Site, SiteItem, Stats } from "@/screens/dashboard/data";
import { NOW } from "./fixtures";
import type { Answer, Entry, Route } from "./entries";

const daysAgo = (n: number, h = 0) => new Date(NOW.getTime() - n * 86400000 - h * 3600000).toISOString();
const day = (n: number) => daysAgo(n).slice(0, 10);
const refuse = (status: number, code: string, fix: string) => new ApiError(status, code, fix);

const ME: Me = {
  id: "p-ana",
  email: "ana@example.com",
  handle: "ana",
  display_name: "Ana's kitchen",
  operator: false,
  home_site: "",
  domain: "",
  domain_status: null,
  push_key: "BEl62iUYgUivxIkv69yViEuiBIa-Ib9-SkvMeAtA3LFgDzkrxZJjSgSnfckjBJuBkr3qBUYIHBQFLXYp5Nksh8U",
  teams: [
    {
      id: "t1",
      handle: "ana-and-ben",
      display_name: "Ana and Ben",
      members: [
        { id: "p-ana", email: "ana@example.com" },
        { id: "p-ben", email: "ben@example.com" },
      ],
      invites: [{ email: "cy@example.com" }],
    },
  ],
  invitations: [],
  keys: [
    { id: "k1", name: "deploy script", last4: "9f2c", last_used_at: daysAgo(1, 2), created_at: daysAgo(60) },
    { id: "k2", name: "old laptop", last4: "a71b", last_used_at: null, created_at: daysAgo(200) },
  ],
  apps: [{ id: "a1", name: "Claude", connected_at: daysAgo(34), last_used_at: daysAgo(0, 3) }],
  devices: [{ id: "d1", name: "Safari on iPhone or iPad" }],
  use: { sites: 5, site_bytes_limit: 50 * 2 ** 20, data_bytes_limit: 10 * 2 ** 20, files_bytes_limit: 100 * 2 ** 20, kept_versions: 5, unlimited: false },
};

const url = (name: string, owner = "ana") => `https://${name}--${owner}.simple-host.site/`;
const item = (name: string, changed: number, o: Partial<SiteItem> = {}): SiteItem => ({
  name,
  owner: "ana",
  url: url(name, o.owner),
  version: 3,
  changed_at: daysAgo(changed, 4),
  offline: false,
  listed: true,
  access: "open",
  ...o,
});

const SITES: SiteItem[] = [
  item("recipes", 0),
  item("bakery", 2, { access: "passcode" }),
  item("club", 5, { access: "viewers", listed: false }),
  item("spring-sale-2026-landing-page-with-a-long-name", 9),
  item("zine", 12, { offline: true }),
  item("gone", 30, { taken_down: true }),
  item("menu", 3, { owner: "ana-and-ben", url: url("menu", "ana-and-ben") }),
];
const BIN: SiteItem[] = [item("old-menu", 10, { deleted_at: daysAgo(2, 1) })];

const versions = [5, 4, 3, 2, 1].map((n) => ({ n, author: n === 4 ? "ben@example.com" : "ana@example.com", at: daysAgo((5 - n) * 6, n) }));

function detail(name: string): Site {
  const it = [...SITES, ...BIN].find((s) => s.name === name) || item(name, 1);
  const base: Site = {
    name,
    owner: it.owner,
    url: it.url,
    live_version: 5,
    versions,
    listed: it.listed,
    offline: it.offline,
    taken_down: !!it.taken_down,
    access: it.access,
    passcode: it.access === "passcode" ? "crumb-42-rye" : "",
    viewers:
      it.access === "viewers"
        ? [
            { email: "ben@example.com", signed_in: true },
            { email: "dana.pottery@example.org", signed_in: false },
          ]
        : [],
    short_name: "",
    domain: null,
    namespaces: [],
    notifications: [],
    use: { data_bytes: 0, data_bytes_limit: ME.use.data_bytes_limit, site_bytes_limit: ME.use.site_bytes_limit },
  };
  if (name === "recipes")
    return {
      ...base,
      short_name: "anas-recipes",
      domain: {
        host: "www.anas-kitchen.com",
        status: "waiting",
        says: "Waiting for the DNS records below. It usually takes a few minutes after you add them, sometimes an hour.",
        records: [
          { type: "CNAME", host: "www.anas-kitchen.com", value: "recipes--ana.simple-host.site", ok: false },
          { type: "TXT", host: "_simple-host.www.anas-kitchen.com", value: "sh-verify=4f1c9a7e2b", ok: true },
        ],
      },
      namespaces: [
        { name: "bookings", bytes: 48_200, rules: { bookings: { read: "owner", add: "anyone" } }, spam_check: true, tables: ["_files", "_values", "bookings"] },
        { name: "guestbook", bytes: 3_100, rules: null, spam_check: false, tables: ["_values", "entries"] },
      ],
      notifications: [{ kind: "data", namespace: "bookings", target: "bookings" }],
      use: { ...base.use, data_bytes: 51_300 },
    };
  return base;
}

function stats(days: number, empty: boolean): Stats {
  const list = Array.from({ length: days }, (_, i) => {
    const d = days - 1 - i;
    const v = empty ? 0 : Math.round(40 + 30 * Math.sin(i / 2.3) + (i % 7 === 5 ? 60 : 0) + i * 1.5);
    return { day: day(d), views: v, people: Math.round(v * 0.62), bots: Math.round(v * 0.4) };
  }).filter(() => !empty);
  const sum = (k: "views" | "people" | "bots") => list.reduce((a, d) => a + d[k], 0);
  return {
    days: list,
    views: sum("views"),
    people: sum("people"),
    bots: sum("bots"),
    pages: empty ? [] : [{ key: "/", n: 1210 }, { key: "/menu", n: 402 }, { key: "/book", n: 266 }, { key: "/about", n: 88 }, { key: "/pie-recipes-for-a-long-winter-weekend", n: 41 }],
    referrers: empty ? [] : [{ key: "google.com", n: 520 }, { key: "instagram.com", n: 310 }, { key: "", n: 280 }, { key: "news.ycombinator.com", n: 12 }],
    countries: empty ? [] : [{ key: "United Kingdom", n: 900 }, { key: "India", n: 401 }, { key: "United States", n: 377 }, { key: "Germany", n: 52 }],
  };
}

type State = { me?: Partial<Me>; sites?: SiteItem[] | Answer; bin?: SiteItem[]; site?: Answer; emptyStats?: boolean; meAnswer?: Answer };

function dashApi(o: State = {}): Route {
  const me = { ...ME, ...o.me };
  return (method, path, body) => {
    const [p, qs] = path.split("?");
    const q = new URLSearchParams(qs);
    if (p === "/v1/me") return o.meAnswer ?? me;
    if (p === "/v1/sites") return q.get("bin") ? { sites: o.bin ?? BIN } : Array.isArray(o.sites) || o.sites === undefined ? { sites: o.sites ?? SITES } : o.sites;
    const m = /^\/v1\/sites\/([^/]+)(\/.*)?$/.exec(p);
    if (m) {
      const name = decodeURIComponent(m[1]);
      const rest = m[2] || "";
      if (rest === "/stats") return stats(Number(q.get("days") || 30), !!o.emptyStats);
      if (rest.endsWith("/query"))
        return {
          columns: ["id", "name", "when", "people", "note", "visitor_id"],
          rows: [
            [12, "Priya", "2026-11-28 19:00", 4, "One vegetarian", "p-91"],
            [11, "Tom", "2026-11-27 12:30", 2, null, "p-88"],
            [10, "Mei and family", "2026-11-26 18:00", 6, "A high chair, please", "p-73"],
          ],
        };
      if (rest.includes("/kv/")) return { items: [{ key: "opening-hours", value: { mon: "closed", tue: "9-17" } }, { key: "visits", value: 1204 }], next_after: "" };
      if (rest.includes("/files/")) return { items: [{ path: "photos/kitchen.jpg", bytes: 182_000, content_type: "image/jpeg" }, { path: "photos/menu-autumn.jpg", bytes: 240_500, content_type: "image/jpeg" }], next_after: "" };
      if (method === "PATCH") return { ...detail(name), ...(body as object) };
      if (o.site !== undefined) return o.site;
      return detail(name);
    }
    if (p === "/v1/keys") return { id: "k3", key: "sh_live_5c0d7e3b9a12f4e8d6c1a0b7f2e9d4c3" };
    if (p.startsWith("/v1/owners/")) return me;
    return {};
  };
}

const site = (name: string, tab = "") => "/dashboard/sites/ana/" + name + (tab ? "#" + tab : "");

export const dashboardEntries: Entry[] = [
  // ---- Your sites
  { id: "dash-loading", title: "Your sites: loading", url: "/dashboard", api: dashApi({ sites: "pending" }) },
  { id: "dash-error", title: "Your sites: could not load", url: "/dashboard", api: dashApi({ sites: refuse(500, "server_error", "Something went wrong on our side. Try again in a minute.") }) },
  { id: "dash-list", title: "Your sites", url: "/dashboard", api: dashApi() },
  { id: "dash-list-offline", title: "Your sites: offline only", url: "/dashboard", api: dashApi(), steps: [{ click: "text=Offline" }] },
  { id: "dash-list-sorted", title: "Your sites: by name", url: "/dashboard", api: dashApi(), steps: [{ select: ["select", "name:asc"] }] },
  { id: "dash-list-nomatch", title: "Your sites: nothing matches", url: "/dashboard", api: dashApi(), steps: [{ fill: ["input[type=search]", "zzz"] }] },
  { id: "dash-invited", title: "Your sites: invited to a team, no app yet", url: "/dashboard", api: dashApi({ me: { apps: [], invitations: [{ id: "i1", team: "kiln-club", display_name: "Kiln club" }] }, bin: [] }) },
  { id: "dash-empty", title: "Your sites: none yet", url: "/dashboard", api: dashApi({ me: { apps: [], teams: [] }, sites: [], bin: [] }) },
  { id: "dash-handle", title: "Your sites: a new account chooses its name", url: "/dashboard", api: dashApi({ me: { handle: "", apps: [], teams: [] }, sites: [], bin: [] }) },
  { id: "dash-new", title: "New site", url: "/dashboard/new", api: dashApi() },
  { id: "dash-new-errors", title: "New site: nothing filled in", url: "/dashboard/new", api: dashApi(), steps: [{ click: "text=Publish" }] },

  // ---- One site
  { id: "site-loading", title: "Site: loading", url: site("recipes"), api: dashApi({ site: "pending" }) },
  { id: "site-notfound", title: "Site: no such site", url: site("nope"), api: dashApi({ site: refuse(404, "not_found", "Not found.") }) },
  { id: "site-visitors", title: "Site: visitors, 30 days", url: site("recipes"), api: dashApi() },
  { id: "site-visitors-7", title: "Site: visitors, 7 days", url: site("recipes"), api: dashApi(), steps: [{ click: "text=7 days" }] },
  { id: "site-visitors-empty", title: "Site: no visitors yet", url: site("bakery"), api: dashApi({ emptyStats: true }) },
  { id: "site-access-open", title: "Site: who can open, anyone", url: site("recipes", "access"), api: dashApi() },
  { id: "site-access-switch", title: "Site: switching to a passcode", url: site("recipes", "access"), api: dashApi(), steps: [{ click: "[role=radio]@1" }] },
  { id: "site-access-passcode", title: "Site: who can open, passcode shown", url: site("bakery", "access"), api: dashApi(), steps: [{ click: "text=Show" }] },
  { id: "site-access-named", title: "Site: who can open, named people", url: site("club", "access"), api: dashApi() },
  {
    id: "site-access-open-ask",
    title: "Site: open to anyone? dialog",
    url: site("club", "access"),
    api: dashApi(),
    steps: [{ click: "[role=radio]@0" }, { click: "text=Save" }],
  },
  { id: "site-files", title: "Site: files and versions", url: site("recipes", "files"), api: dashApi() },
  { id: "site-files-undo", title: "Site: an older version made live, with Undo", url: site("recipes", "files"), api: dashApi(), steps: [{ click: "text=Make live" }] },
  { id: "site-files-error", title: "Site: publish with no files", url: site("recipes", "files"), api: dashApi(), steps: [{ click: "text=Publish" }] },
  { id: "site-data", title: "Site: saved data", url: site("recipes", "data"), api: dashApi() },
  { id: "site-data-rows", title: "Site: a table's rows", url: site("recipes", "data"), api: dashApi(), steps: [{ click: "text=Show rows" }] },
  { id: "site-data-empty", title: "Site: no saved data", url: site("bakery", "data"), api: dashApi() },
  { id: "site-address", title: "Site: short address and own domain waiting for DNS", url: site("recipes", "address"), api: dashApi() },
  { id: "site-address-empty", title: "Site: no extra addresses", url: site("bakery", "address"), api: dashApi() },
  { id: "site-alerts", title: "Site: alerts", url: site("recipes", "alerts"), api: dashApi() },
  { id: "site-settings", title: "Site: settings", url: site("recipes", "settings"), api: dashApi() },
  { id: "site-settings-delete", title: "Site: delete dialog", url: site("recipes", "settings"), api: dashApi(), steps: [{ click: "text=Delete recipes" }] },
  { id: "site-offline", title: "Site: offline", url: site("zine"), api: dashApi() },
  { id: "site-takendown", title: "Site: taken down", url: site("gone"), api: dashApi() },

  // ---- Account
  { id: "account-profile", title: "Account: profile", url: "/dashboard/account", api: dashApi() },
  {
    id: "account-handle-ask",
    title: "Account: change name? dialog",
    url: "/dashboard/account",
    api: dashApi(),
    steps: [{ fill: ["#handle", "ana-cooks"] }, { click: "text=Save" }],
  },
  { id: "account-keys", title: "Account: keys and apps", url: "/dashboard/account#keys", api: dashApi() },
  {
    id: "account-key-made",
    title: "Account: a new key, shown once",
    url: "/dashboard/account#keys",
    api: dashApi(),
    steps: [{ fill: ["#key-name", "shop sync"] }, { click: "text=Make key" }],
  },
  { id: "account-notifications", title: "Account: notifications", url: "/dashboard/account#notifications", api: dashApi() },
  { id: "account-teams", title: "Account: teams", url: "/dashboard/account#teams", api: dashApi() },
  { id: "account-data", title: "Account: your data", url: "/dashboard/account#data", api: dashApi() },
  {
    id: "account-delete",
    title: "Account: delete dialog",
    url: "/dashboard/account#data",
    api: dashApi(),
    steps: [{ click: "text=Delete my account…" }, { fill: ["#confirm-email", "ana@example"] }, { click: "text=Delete my account" }],
  },
];
