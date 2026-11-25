// Fixture data for the catalogue: plausible rows in the shapes the /v1 routes
// answer. Times are relative to NOW, which the visual checks freeze the clock at.
import type { Growth, Overview, Person, Site } from "@/screens/admin/data";

export const NOW = new Date("2026-11-25T12:00:00Z");
const daysAgo = (n: number, h = 0) => new Date(NOW.getTime() - n * 86400000 - h * 3600000).toISOString();
const day = (n: number) => daysAgo(n).slice(0, 10);

type RawPerson = Omit<Person, "n" | "siteList">;

function person(id: number, handle: string, joined: number, o: Partial<RawPerson> = {}): RawPerson {
  return {
    id: "p" + id,
    email: (handle || "someone" + id) + "@example.com",
    handle,
    display_name: null,
    created_at: daysAgo(joined, 3),
    operator: false,
    suspended: false,
    suspended_reason: null,
    sites: 0,
    bytes: 0,
    views_30d: 0,
    last_seen: daysAgo(Math.max(0, joined - 2), 1),
    ...o,
  };
}

function site(id: number, name: string, owner: string, o: Partial<Site> = {}): Site {
  return {
    id: "s" + id,
    name,
    owner_id: "p-" + owner,
    owner,
    created_at: daysAgo(40 + id, 2),
    changed_at: daysAgo(id, 5),
    live_version: 3,
    listed: true,
    offline: false,
    access: "open",
    taken_down: false,
    taken_down_reason: null,
    owner_suspended: false,
    live_bytes: 180_000 + id * 91_337,
    bytes: 640_000 + id * 391_337,
    views_30d: Math.max(0, 900 - id * 70),
    people_30d: Math.max(0, 300 - id * 25),
    domain: "",
    domain_status: "",
    short_name: "",
    url: `https://${name}.${owner}.simple-host.app/`,
    ...o,
  };
}

const PEOPLE: RawPerson[] = [
  person(1, "ana", 1, { sites: 5, bytes: 2_400_000, views_30d: 2210, display_name: "Ana Lima" }),
  person(2, "ben", 3, { sites: 1, bytes: 410_000, views_30d: 120 }),
  person(3, "cy", 9, { sites: 0, last_seen: null }),
  person(4, "sam", 15, { sites: 1, bytes: 220_000, suspended: true, suspended_reason: "Phishing page pretending to be a bank" }),
  person(5, "dana-pottery-and-ceramics-studio", 40, { sites: 2, bytes: 9_800_000, views_30d: 5120 }),
  person(6, "", 61, { email: "no-handle-yet@example.org" }),
  person(7, "eli", 200, { sites: 1, bytes: 120_000, views_30d: 3 }),
  person(8, "admin", 400, { operator: true }),
];
PEOPLE.forEach((p) => (p.id = "p-" + (p.handle || p.id)));

const SITES: Site[] = [
  site(1, "recipes", "ana", { domain: "recipes.analima.com", domain_status: "live", views_30d: 1840, people_30d: 610 }),
  site(2, "bakery", "ana", { access: "passcode", listed: false }),
  site(3, "club", "ana", { access: "viewers", short_name: "club" }),
  site(4, "zine", "ana", { offline: true }),
  site(5, "gone", "ana", { taken_down: true, taken_down_reason: "Copied a whole newspaper's paywalled articles", views_30d: 0 }),
  site(6, "portfolio", "ben", { domain: "ben.studio", domain_status: "pending" }),
  site(7, "shop", "sam", { owner_suspended: true, views_30d: 0 }),
  site(8, "spring-sale-2026-landing-page-with-a-long-name", "dana-pottery-and-ceramics-studio", { views_30d: 4800, bytes: 8_900_000 }),
  site(9, "glazes", "dana-pottery-and-ceramics-studio", { live_version: 0, url: undefined, views_30d: 0 }),
  site(10, "notes", "eli", { views_30d: 3, listed: false }),
];

function growth(range = "30d", empty = false): Growth {
  const days = { "7d": 7, "14d": 14, "30d": 30, "6m": 182 }[range] || 30;
  const dates = Array.from({ length: days }, (_, i) => day(days - 1 - i));
  const wave = (base: number, k: number) => dates.map((_, i) => (empty ? 0 : Math.round(base + base * 0.6 * Math.sin((i + k) / 3) + (i % 7 === 0 ? base * 0.4 : 0))));
  const total = (a: number[]) => a.reduce((x, y) => x + y, 0);
  const api = wave(420, 1);
  const users = wave(3, 2);
  const sites = wave(5, 4);
  return {
    range,
    days,
    from: dates[0],
    to: dates[days - 1],
    dates,
    tracking_since: empty ? "" : day(400),
    self_calls: 0,
    group_order: ["calls"],
    api: { total: total(api), previous: empty ? 0 : 9800, daily: api, change_pct: empty ? null : 31, groups: { calls: api } },
    users: { total: total(users), previous: empty ? 0 : 61, daily: users, change_pct: empty ? null : 48 },
    sites: { total: total(sites), previous: empty ? 0 : 160, daily: sites, change_pct: empty ? null : -4 },
    countries: empty
      ? []
      : [
          { code: "US", name: "United States", calls: 6120 },
          { code: "IN", name: "India", calls: 3310 },
          { code: "GB", name: "United Kingdom", calls: 1490 },
          { code: "DE", name: "Germany", calls: 820 },
          { code: "BR", name: "Brazil", calls: 640 },
          { code: "JP", name: "Japan", calls: 310 },
          { code: "NG", name: "Nigeria", calls: 190 },
          { code: "AU", name: "Australia", calls: 120 },
          { code: "CA", name: "Canada", calls: 95 },
          { code: "FR", name: "France", calls: 40 },
          { code: "KE", name: "Kenya", calls: 6 },
        ],
    unknown_country_calls: empty ? 0 : 212,
  };
}

function overview(o: Partial<Overview> = {}): Overview {
  return {
    self_hosted: false,
    people: { total: PEOPLE.length, new_7d: 2, new_30d: 4 },
    reports: [
      { id: 2, url: "https://gone.ana.simple-host.app/", reason: "This copies paywalled newspaper articles word for word.", created_at: daysAgo(2), owner: "ana", site: "gone" },
      { id: 1, url: "https://recipes.ana.simple-host.app/pie", reason: "Spam", created_at: daysAgo(6), owner: "ana", site: "recipes" },
    ],
    network: Array.from({ length: 30 }, (_, i) => ({ day: day(29 - i), bytes_in: 40_000_000 + i * 1_000_000, bytes_out: 900_000_000 + i * 20_000_000 })),
    growth: growth(),
    ...o,
  };
}

/** Admin is the three admin answers for one state of the page. */
export function admin(state: "full" | "empty" | "selfhosted" | "many") {
  let people = PEOPLE;
  let sites = SITES;
  let ov = overview();
  if (state === "empty") {
    people = [PEOPLE[PEOPLE.length - 1]];
    sites = [];
    ov = overview({ people: { total: 1, new_7d: 0, new_30d: 0 }, reports: [], network: [], growth: growth("30d", true) });
  }
  if (state === "selfhosted") ov = overview({ self_hosted: true });
  if (state === "many") {
    sites = Array.from({ length: 120 }, (_, i) => site(i + 1, "site-" + String(i + 1).padStart(3, "0"), PEOPLE[i % 5].handle));
  }
  return {
    overview: ov,
    people: { people },
    sites: { sites },
    growth: (range: string) => ({ ...ov, growth: state === "empty" ? growth(range, true) : growth(range) }),
  };
}
