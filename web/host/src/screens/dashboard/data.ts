// The owner dashboard's data. Every read and change goes through the same /v1
// routes the AI app uses:
//   GET   /v1/me                    the person, teams, invitations, keys, apps, devices, use
//   PATCH /v1/owners/{owner}        handle, display name, home site, one domain for all sites
//   GET   /v1/sites[?bin=true]      my sites and my teams' sites (or the bin)
//   GET   /v1/sites/{site}?owner=   everything about one site; PATCH changes it and answers the same
//   GET   /v1/sites/{site}/stats?days=
//   PUT   /v1/sites/{site}          publish files; DELETE moves it to the bin
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useApi, type Call } from "@/lib/api";
import { useToast } from "@kit/components/ui/toast";

export type Team = { id: string; handle: string; display_name: string; members: { id: string; email: string }[]; invites: { email: string }[] };
export type DomainState = {
  host: string;
  status: string;
  says?: string;
  error?: string;
  records?: { type: string; host: string; value: string; ok: boolean; optional?: boolean }[];
};
export type Me = {
  id: string;
  email: string;
  handle: string;
  display_name: string;
  operator: boolean;
  home_site: string;
  domain: string;
  domain_status: DomainState | null;
  push_key: string;
  teams: Team[];
  invitations: { id: string; team: string; display_name: string }[];
  keys: { id: string; name: string; last4: string; last_used_at: string | null; created_at?: string }[];
  apps: { id: string; name: string; connected_at: string; last_used_at?: string | null }[];
  devices: { id: string; name: string }[];
  use: { sites: number; site_bytes_limit: number; data_bytes_limit: number; files_bytes_limit: number; kept_versions: number; unlimited: boolean };
};

export type Access = "open" | "passcode" | "viewers";
export type SiteItem = {
  name: string;
  owner: string;
  url: string;
  version: number;
  changed_at: string;
  offline: boolean;
  listed: boolean;
  access: Access;
  deleted_at?: string;
  taken_down?: boolean;
};
export type Namespace = { name: string; bytes: number; rules: Record<string, unknown> | null; spam_check: boolean; tables: string[] };
export type Rule = { kind: "access" | "data"; namespace?: string; target?: string };
export type Site = {
  name: string;
  owner: string;
  url: string;
  live_version: number;
  versions: { n: number; author: string; at: string }[];
  listed: boolean;
  offline: boolean;
  taken_down: boolean;
  access: Access;
  passcode: string;
  viewers: { email: string; signed_in: boolean }[];
  short_name: string;
  domain: DomainState | null;
  namespaces: Namespace[];
  notifications: Rule[];
  use: { data_bytes: number; data_bytes_limit: number; site_bytes_limit: number };
};
export type Count = { key: string; n: number };
export type Stats = {
  days: { day: string; views: number; people: number; bots: number }[];
  views: number;
  people: number;
  bots: number;
  pages: Count[];
  referrers: Count[];
  countries: Count[];
};

export const ACCESS_WORDS: Record<Access, string> = { open: "Anyone with the link", passcode: "Passcode", viewers: "Named people" };

/** sitePath is a site's route: /v1/sites/{name}[rest], with ?owner= for a team's site. */
export function sitePath(s: { name: string; owner: string }, me: { handle: string } | undefined, rest = "", params?: Record<string, string>) {
  const p = new URLSearchParams(params);
  if (s.owner && s.owner !== me?.handle) p.set("owner", s.owner);
  const q = p.toString();
  return "/v1/sites/" + encodeURIComponent(s.name) + rest + (q ? "?" + q : "");
}

/** pagePath is the dashboard's page of a site. */
export const pagePath = (s: { name: string; owner: string }, tab = "") =>
  "/dashboard/sites/" + encodeURIComponent(s.owner) + "/" + encodeURIComponent(s.name) + (tab ? "#" + tab : "");

/** host is an address without https:// and the last slash. */
export const host = (url: string) => url.replace(/^https?:\/\//, "").replace(/\/$/, "");

/** publicPage is the owner's own page, read off one of their sites' addresses. */
export function publicPage(siteUrl: string, name: string, handle: string): string {
  const h = host(siteUrl);
  for (const prefix of [name + "." + handle + ".", name + "--" + handle + "."]) {
    if (h.startsWith(prefix)) return "https://" + handle + "." + h.slice(prefix.length) + "/";
  }
  return "";
}

export function useMe() {
  const call = useApi();
  return useQuery({ queryKey: ["me"], queryFn: () => call<Me>("GET", "/v1/me"), retry: false, staleTime: 30_000 });
}

export function useSites(bin = false) {
  const call = useApi();
  return useQuery({
    queryKey: ["sites", bin ? "bin" : "live"],
    queryFn: () => call<{ sites: SiteItem[] }>("GET", "/v1/sites" + (bin ? "?bin=true" : "")).then((b) => b.sites || []),
  });
}

export function useSite(owner: string, name: string) {
  const call = useApi();
  const me = useMe().data;
  return useQuery({
    queryKey: ["site", owner, name],
    queryFn: () => call<Site>("GET", sitePath({ owner, name }, me)),
    enabled: !!me,
  });
}

export function useStats(site: Site, days: number) {
  const call = useApi();
  const me = useMe().data;
  return useQuery({
    queryKey: ["stats", site.owner, site.name, days],
    queryFn: () => call<Stats>("GET", sitePath(site, me, "/stats", { days: String(days) })),
  });
}

/**
 * useChange returns change(body, done, undo?): PATCH the site, put the
 * answer in place, and say done in a toast (with Undo when given). It
 * resolves to true when it worked.
 */
export function useChange(site: { name: string; owner: string }) {
  const call = useApi();
  const qc = useQueryClient();
  const toast = useToast();
  const me = useMe().data;
  const m = useMutation({ mutationFn: (body: unknown) => call<Site>("PATCH", sitePath(site, me), body) });
  return async (body: unknown, done: string, undo?: () => void): Promise<boolean> => {
    try {
      const next = await m.mutateAsync(body);
      qc.setQueryData(["site", site.owner, site.name], next);
      void qc.invalidateQueries({ queryKey: ["sites"] });
      toast(done, undo && { undo: { label: "Undo", run: undo } });
      return true;
    } catch (e) {
      toast((e as Error).message, { bad: true });
      return false;
    }
  };
}

/** readFiles turns chosen files (or a folder) into {path: base64}. A folder loses its own name. */
export async function readFiles(list: FileList | File[]): Promise<Record<string, string>> {
  const pairs = await Promise.all(
    Array.from(list).map(
      (f) =>
        new Promise<[File, string]>((resolve, reject) => {
          const r = new FileReader();
          r.onload = () => resolve([f, String(r.result).replace(/^data:[^,]*,/, "")]);
          r.onerror = () => reject(new Error("Could not read " + f.name + "."));
          r.readAsDataURL(f);
        }),
    ),
  );
  const out: Record<string, string> = {};
  for (const [f, b64] of pairs) {
    const path = (f.webkitRelativePath || f.name).split("/");
    if (path.length > 1) path.shift();
    out[path.join("/")] = b64;
  }
  return out;
}

/** publish sends files as the site's next version. keep leaves every other file as it is. */
export async function publish(call: Call, site: { name: string; owner: string }, me: Me | undefined, files: FileList | File[], keep: boolean) {
  const b64 = await readFiles(files);
  if (!Object.keys(b64).length) throw new Error("Choose at least one file.");
  return call<{ url?: string }>("PUT", sitePath(site, me, "", keep ? { keep_other_files: "true" } : undefined), { files_base64: b64 });
}

/** validName is the rule for a site name: lower-case letters, digits and single hyphens. */
export const validName = (n: string) => /^[a-z0-9](?:[a-z0-9]|-(?=[a-z0-9])){0,62}$/.test(n);

/** pushKey turns the service's base64url public key into bytes for the browser. */
export function pushKey(s: string): Uint8Array<ArrayBuffer> {
  const raw = atob((s + "=".repeat((4 - (s.length % 4)) % 4)).replace(/-/g, "+").replace(/_/g, "/"));
  const out = new Uint8Array(new ArrayBuffer(raw.length));
  for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i);
  return out;
}
