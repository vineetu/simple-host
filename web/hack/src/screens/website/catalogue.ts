// Website in every state: loading, did not load, no team, nothing published
// (each app's steps, other apps), connected and waiting, a teammate
// connected, setting up, published (with notifications and deliveries),
// taken down, closed after the deadline, a zip refused, and each dialog.
import { refuse, type Entry } from "@kit/catalogue/harness";
import { api } from "@/catalogue/fixtures";
import { LUMBUNG, on } from "../team/catalogue";
import type { HookList, SiteStatus } from "./data";

const SLUG = "jakarta-ai-night";
const url = `/e/${SLUG}/team?s=website`;
const E = "/v1/hack/events/" + SLUG;
const SITE = E + "/site";
const HOOKS = "/v1/sites/lumbung/notifications?event=" + SLUG;

const status = (site: SiteStatus["site"], connection: SiteStatus["connection"] = { state: "none", apps: [], teammates_connected: [] }): SiteStatus => ({
  site: { exists: false, ready: true, url: "https://lumbung.jakarta-ai-night.simple-hack.app/", ...site },
  connection,
  api_base: "https://simple-hack.app",
  mcp_url: "https://simple-hack.app/mcp",
  upload_limits: "Up to 25 MB, 2,000 files.",
});
const live = { exists: true, live_version: 3, published_at: "2026-11-20T09:48:00Z", published_via: "Claude" };
const claude = { state: "connected", app: "Claude", apps: ["Claude", "Bolt"], last_used_at: "2026-11-20T09:56:00Z", teammates_connected: ["Dian"] };
const hooks: HookList = {
  limits: { enabled: true, can_manage: true, can_create: true },
  notifications: [
    {
      id: "h1",
      source: "owner",
      label: "Our sign-up sheet",
      url: "https://hooks.example.com/lumbung",
      events: ["record.added", "record.changed"],
      storage: "signups",
      state: "active",
      last_attempt_at: "2026-11-20T09:30:00Z",
      last_result: "ok",
    },
    { id: "h2", source: "mcp", app_name: "Claude", events: ["file.uploaded"], state: "paused" },
  ],
};
const entry = { entry: { problem: "Village co-ops lose track of who lent what", tagline: "A shared ledger farmers keep by voice message." } };
const page = (s: unknown, o: Record<string, unknown> = {}) => api({ ["GET " + E]: on(LUMBUNG), [SITE]: s, [E + "/entry"]: entry, [HOOKS]: hooks, ...o });
const open = (n: number) => ({ click: `details summary@${n}` });

export const entries: Entry[] = [
  { id: "participant-website.loading", title: "Website: loading", url, api: page("pending") },
  { id: "participant-website.error", title: "Website: did not load", url, api: page(refuse(500, "error", "Something went wrong. Try again.")) },
  { id: "participant-website.no-team", title: "Website: no team yet", url, api: page(status({}), { ["GET " + E]: on(null) }) },
  { id: "participant-website.unpublished", title: "Website: not connected, nothing published (Claude)", url, api: page(status({})) },
  { id: "participant-website.chatgpt", title: "Website: the ChatGPT steps", url, api: page(status({})), steps: [{ click: "text=ChatGPT" }] },
  { id: "participant-website.other-apps", title: "Website: other AI apps", url, api: page(status({})), steps: [{ click: "text=Other AI apps" }] },
  { id: "participant-website.connected", title: "Website: connected, waiting for the first publish", url, api: page(status({}, claude)) },
  { id: "participant-website.setting-up", title: "Website: the address is being set up", url, api: page(status({ ready: false })) },
  { id: "participant-website.zip-refused", title: "Website: Upload with no zip chosen", url, api: page(status({})), steps: [{ click: "text=Upload" }] },
  { id: "participant-website.advanced", title: "Website: command line", url, api: page(status({})), steps: [open(0)] },
  { id: "participant-website.published", title: "Website: published, connected in Claude", url, api: page(status(live, claude)) },
  { id: "participant-website.publish-again", title: "Website: published, Publish again open", url, api: page(status(live, claude)), steps: [open(0)] },
  { id: "participant-website.notifications", title: "Website: notifications", url, api: page(status(live, claude)), steps: [open(2)] },
  {
    id: "participant-website.deliveries",
    title: "Website: a notification's deliveries",
    url,
    api: page(status(live, claude), {
      ["/v1/sites/lumbung/notifications/h1/deliveries?event=" + SLUG]: {
        deliveries: [
          { name: "record.added", created_at: "2026-11-20T09:30:00Z", state: "delivered", result: "ok", last_http: 200, attempts: 1 },
          { name: "record.changed", created_at: "2026-11-20T08:10:00Z", state: "failed", result: "timeout", attempts: 3 },
        ],
      },
    }),
    steps: [open(2), { click: "text=Deliveries" }],
  },
  {
    id: "participant-website.secret",
    title: "Website: a notification added, its secret shown once",
    url,
    api: page(status(live, claude), { ["POST " + HOOKS]: { secret: "whsec_7f3a9c2e41b84d0f" } }),
    steps: [open(2), { fill: ["#notif-url", "https://hooks.example.com/new"] }, { click: "text=Add notification" }],
  },
  { id: "participant-website.remove-hook", title: "Website: remove a notification", url, api: page(status(live, claude)), steps: [open(2), { click: "text=Remove" }] },
  { id: "participant-website.rotate", title: "Website: rotate a signing secret", url, api: page(status(live, claude)), steps: [open(2), { click: "text=Rotate secret" }] },
  { id: "participant-website.taken-down", title: "Website: the site was taken down", url, api: page(status({ ...live, taken_down: true, taken_down_reason: "Taken down by the organiser: copyrighted images." })) },
  {
    id: "participant-website.closed-pinned",
    title: "Website: publishing closed, nothing live but a pinned version",
    url,
    api: page(status({ pinned_version: 2, pinned_at: "2026-11-19T11:00:00Z" }), { ["GET " + E]: on({ ...LUMBUNG, frozen: true }, "judging") }),
  },
  {
    id: "participant-website.closed-nothing",
    title: "Website: nothing published by the deadline",
    url,
    api: page(status({}), { ["GET " + E]: on({ ...LUMBUNG, frozen: true }, "judging") }),
  },
  {
    id: "participant-website.closed-live",
    title: "Website: closed, the site stays live as submitted",
    url,
    api: page(status(live), { ["GET " + E]: on({ ...LUMBUNG, frozen: true }, "judging") }),
  },
];
