// The admin page's data: GET /v1/admin/overview, /v1/admin/people and
// /v1/admin/sites, every row at once, so the page sorts and filters itself.
import { useQuery } from "@tanstack/react-query";
import { useApi, type Call } from "@/lib/api";

export type Person = {
  id: string;
  email: string;
  handle: string;
  display_name: string | null;
  created_at: string;
  operator: boolean;
  suspended: boolean;
  suspended_reason: string | null;
  sites: number;
  bytes: number;
  views_30d: number;
  last_seen: string | null;
  /** Filled here: 1 is the newest joiner. */
  n: number;
  siteList: Site[];
};

export type Site = {
  id: string;
  name: string;
  owner_id: string;
  owner: string;
  created_at: string;
  changed_at: string;
  live_version: number;
  listed: boolean;
  offline: boolean;
  access: "open" | "passcode" | "viewers" | string;
  taken_down: boolean;
  taken_down_reason: string | null;
  owner_suspended: boolean;
  live_bytes: number;
  bytes: number;
  views_30d: number;
  people_30d: number;
  domain: string;
  domain_status: string;
  short_name: string;
  url?: string;
};

export type Report = { id: number; url: string; reason: string; created_at: string; owner: string; site: string };

export type Series = { total: number; previous: number; daily: number[]; change_pct: number | null };

export type Growth = {
  range: string;
  days: number;
  from: string;
  to: string;
  dates: string[];
  tracking_since: string;
  self_calls: number;
  group_order: string[];
  api: Series & { groups: Record<string, number[]> };
  users: Series;
  sites: Series;
  countries: { code: string; name: string; calls: number }[];
  unknown_country_calls: number;
};

export type Overview = {
  self_hosted: boolean;
  people: { total: number; new_7d: number; new_30d: number };
  reports: Report[];
  network: { day: string; bytes_in: number; bytes_out: number }[];
  growth: Growth;
};

export type AdminData = { overview: Overview; people: Person[]; sites: Site[]; byHandle: Record<string, Person> };

export async function loadAdmin(call: Call): Promise<AdminData> {
  const overview = await call<Overview>("GET", "/v1/admin/overview");
  const [p, s] = await Promise.all([
    call<{ people: Omit<Person, "n" | "siteList">[] }>("GET", "/v1/admin/people"),
    call<{ sites: Site[] }>("GET", "/v1/admin/sites"),
  ]);
  const people: Person[] = (p.people || []).map((x) => ({ ...x, n: 0, siteList: [] }));
  const byId: Record<string, Person> = {};
  const byHandle: Record<string, Person> = {};
  people.forEach((x) => {
    byId[x.id] = x;
    if (x.handle) byHandle[x.handle] = x;
  });
  [...people]
    .sort((a, b) => Date.parse(b.created_at) - Date.parse(a.created_at))
    .forEach((x, i) => (x.n = i + 1));
  const sites = s.sites || [];
  sites.forEach((x) => byId[x.owner_id]?.siteList.push(x));
  return { overview, people, sites, byHandle };
}

export function useAdmin() {
  const call = useApi();
  return useQuery({ queryKey: ["admin"], queryFn: () => loadAdmin(call), retry: false });
}

export function useGrowth(range: string) {
  const call = useApi();
  return useQuery({
    queryKey: ["growth", range],
    queryFn: async () => (await call<Overview>("GET", "/v1/admin/overview?range=" + encodeURIComponent(range))).growth,
    placeholderData: (prev) => prev,
  });
}

export const siteLive = (s: Site) => s.live_version > 0 && !s.offline && !s.taken_down && !s.owner_suspended;
export const ownerName = (s: Site) => s.owner || "(no handle)";
export const dashLink = (s: Site) => "/dashboard#site/" + encodeURIComponent(s.owner) + "/" + encodeURIComponent(s.name);
export const ACCESS: Record<string, string> = { open: "anyone", passcode: "passcode", viewers: "named people" };

/** siteFilter is the Sites tab's chips. */
export const SITE_FILTERS = [
  ["all", "All"],
  ["down", "Taken down"],
  ["offline", "Offline"],
  ["locked", "Passcode or named people"],
  ["domain", "Has own address"],
  ["unlisted", "Not on profile"],
] as const;
export type SiteFilter = (typeof SITE_FILTERS)[number][0];

export function siteFilter(f: SiteFilter, s: Site): boolean {
  switch (f) {
    case "down":
      return s.taken_down || s.owner_suspended;
    case "offline":
      return s.offline;
    case "locked":
      return s.access !== "open";
    case "domain":
      return !!(s.domain || s.short_name);
    case "unlisted":
      return !s.listed;
  }
  return true;
}

export const USER_FILTERS = [
  ["all", "All"],
  ["sites", "Has sites"],
  ["suspended", "Suspended"],
] as const;
export type UserFilter = (typeof USER_FILTERS)[number][0];

export function userFilter(f: UserFilter, u: Person): boolean {
  if (f === "sites") return u.sites > 0;
  if (f === "suspended") return u.suspended;
  return true;
}
