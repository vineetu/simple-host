// Account and Create event in every state. Ids are the UX atlas's screen.state ids.
import { refuse, type Entry } from "@kit/catalogue/harness";
import { api, me } from "@/catalogue/fixtures";
import { site } from "./data";

// The pictures must not depend on the port the catalogue is served from.
site.origin = () => "https://simple-hack.app";
site.host = () => "simple-hack.app";

const ME = { ...me, username: "siti@example.com", has_password: true, login: "siti", signin_alerts: true };
const KEY_ONLY = { id: "u-key", username: "siti-key", display_name: "Siti Rahma" };
const IDS = "/v1/me/identities";
const CONNS = "/v1/me/connections";
const PREFS = "/v1/hack/preferences";
const PROVIDERS = "/v1/auth/oauth/providers";

const steps = {
  apps: [
    {
      id: "claude",
      label: "Claude",
      tab: true,
      connect: [{ html: "Open Claude, then <b>Settings</b>, then <b>Connectors</b>, then <b>Add custom connector</b>." }, { html: "Name it <b>Simple Hack</b>, paste this address, and choose <b>Add</b>:", address: true }],
      connect_hosted: [
        { html: "Open Claude, then <b>Settings</b>, then <b>Connectors</b>, then <b>Browse connectors</b>." },
        { html: "Search for <b>Simple Hack</b>, choose <b>Connect</b>, sign in, then choose <b>Allow</b>." },
      ],
      fallback: "On a network that blocks the directory, add it as a custom connector instead: name it Simple Hack, paste this address:",
    },
    { id: "chatgpt", label: "ChatGPT", tab: true, connect: [{ html: "Name it <b>Simple Hack</b>, and paste this address as the <b>MCP server URL</b>:", address: true }] },
    { id: "claude-code", label: "Claude Code", tab: true, connect: [{ html: "Run this in your terminal:", command: "claude mcp add --transport http simple-hack {address}" }] },
    { id: "grok", label: "Grok", connect: [] },
    { id: "cursor", label: "Cursor", connect: [] },
    { id: "other", label: "Any other AI app", connect: [] },
  ],
};

const base = {
  "/v1/me": ME,
  [IDS]: { identities: [{ id: "i1", provider: "google", email: "siti.rahma@gmail.com", linked_at: "2026-10-03T08:00:00Z" }] },
  [CONNS]: {
    connections: [
      { client_id: "c1", name: "Claude", device: "Mac", last_used_at: "2026-11-20T08:00:00Z", teams: [{ team: "Lumbung", event: "Jakarta AI Night" }] },
      { client_id: "c2", name: "ChatGPT", last_used_at: "2026-11-17T10:00:00Z" },
    ],
  },
  [PREFS]: { email_updates: true, email_possible: true },
  [PROVIDERS]: { providers: ["google"], email_enabled: true },
  "/hack-connect-steps.json": steps,
};
const acct = (o: Record<string, unknown> = {}) => api({ ...base, ...o });

export const entries: Entry[] = [
  { id: "account.loading", title: "Account: loading", url: "/account", api: acct({ "/v1/me": "pending" }) },
  { id: "account.error", title: "Account: did not load", url: "/account", api: acct({ "/v1/me": refuse(500, "error", "Something went wrong. Try again.") }) },
  { id: "account.not-a-person", title: "Account: a team key or the platform admin", url: "/account", api: acct({ "/v1/me": { username: "admin" } }) },
  { id: "account.leave-unsaved", title: "Account: leaving with an unsaved name asks first", url: "/account", api: acct(), steps: [{ fill: ["#acct-name", "Siti R."] }, { click: "text=Your events" }] },
  { id: "account.loaded", title: "Account: profile (name, email, password, delete)", url: "/account", api: acct() },
  { id: "account.key-only", title: "Account: a key-only account, no email address", url: "/account", api: acct({ "/v1/me": KEY_ONLY }) },
  {
    id: "account.name-refused",
    title: "Account: the name is refused, said under it",
    url: "/account",
    api: acct({ "PATCH /v1/me": refuse(400, "invalid_display_name", "Enter your name on one line, without special formatting.") }),
    steps: [{ fill: ["#acct-name", "Siti <b>"] }, { click: "text=Save" }],
  },
  { id: "account.email-change", title: "Account: change email, the new address", url: "/account", api: acct(), steps: [{ click: "text=Change email" }] },
  {
    id: "account.email-codes",
    title: "Account: change email, the two codes",
    url: "/account",
    api: acct({ "POST /v1/me/email": { email: "siti.new@example.com", current_email: "siti@example.com" } }),
    steps: [{ click: "text=Change email" }, { fill: ["#acct-email-input", "siti.new@example.com"] }, { click: "text=Send codes" }],
  },
  {
    id: "account.email-codes-missing",
    title: "Account: change email, codes not filled in",
    url: "/account",
    api: acct({ "POST /v1/me/email": { email: "siti.new@example.com", current_email: "siti@example.com" } }),
    steps: [{ click: "text=Change email" }, { fill: ["#acct-email-input", "siti.new@example.com"] }, { click: "text=Send codes" }, { fill: ["#acct-code", "12"] }, { click: "text=Change email" }],
  },
  {
    id: "account.password-mismatch",
    title: "Account: password, the new ones do not match",
    url: "/account",
    api: acct(),
    steps: [{ fill: ["#acct-password-current", "old-password"] }, { fill: ["#acct-password-new", "new-password-1"] }, { fill: ["#acct-password-confirm", "new-password-2"] }, { click: "text=Change password" }],
  },
  { id: "account.delete", title: "Account: delete, type to confirm", url: "/account", api: acct(), steps: [{ click: "text=Delete account" }] },
  {
    id: "account.delete-blocked",
    title: "Account: delete refused while organising",
    url: "/account",
    api: acct({ "DELETE /v1/me": refuse(409, "organiser_active", "You're organising Green Campus Hack. End it or hand it over first.") }),
    steps: [{ click: "text=Delete account" }, { fill: ["#acct-delete-input", "siti"] }, { click: "[role=dialog] button[type=submit]" }],
  },
  {
    id: "account.deleted",
    title: "Account: deleted",
    url: "/account",
    api: acct({ "DELETE /v1/me": {} }),
    steps: [{ click: "text=Delete account" }, { fill: ["#acct-delete-input", "siti"] }, { click: "[role=dialog] button[type=submit]" }],
  },
  { id: "account.sign-in", title: "Account: sign-in, Google linked", url: "/account#sign-in", api: acct() },
  { id: "account.sign-in-none", title: "Account: sign-in, nothing linked, Link Google", url: "/account#sign-in", api: acct({ [IDS]: { identities: [] } }) },
  { id: "account.sign-in-loading", title: "Account: sign-in, linked sign-ins loading", url: "/account#sign-in", api: acct({ [IDS]: "pending" }) },
  { id: "account.sign-in-error", title: "Account: sign-in, linked sign-ins did not load", url: "/account#sign-in", api: acct({ [IDS]: refuse(500, "error", "Something went wrong. Try again.") }) },
  { id: "account.link-google", title: "Account: link Google, asked first", url: "/account#sign-in", api: acct({ [IDS]: { identities: [] } }), steps: [{ click: "text=Link Google" }] },
  { id: "account.unlink", title: "Account: unlink Google, asked first", url: "/account#sign-in", api: acct(), steps: [{ click: "text=Unlink" }] },
  { id: "account.new-key", title: "Account: make a new key, asked first", url: "/account#sign-in", api: acct(), steps: [{ click: "text=New key" }] },
  {
    id: "account.new-key-shown",
    title: "Account: the new key, shown once",
    url: "/account#sign-in",
    api: acct({ "POST /v1/me/api-key/rotate": { api_key: "sh_live_8f2c1d9e4b7a6c5d3e2f1a0b9c8d7e6f" } }),
    steps: [{ click: "text=New key" }, { click: "[role=dialog] button[type=submit]" }],
  },
  { id: "account.sign-out-everywhere", title: "Account: sign out everywhere, asked first", url: "/account#sign-in", api: acct(), steps: [{ click: "text=Sign out everywhere" }] },
  { id: "account.apps-loading", title: "Account: AI apps loading", url: "/account#apps", api: acct({ [CONNS]: "pending" }) },
  { id: "account.apps-none", title: "Account: no AI apps, the steps open", url: "/account#apps", api: acct({ [CONNS]: { connections: [] } }) },
  { id: "account.apps-connected", title: "Account: connected AI apps", url: "/account#apps", api: acct() },
  { id: "account.apps-error", title: "Account: AI apps did not load", url: "/account#apps", api: acct({ [CONNS]: refuse(500, "error", "Something went wrong. Try again.") }) },
  {
    id: "account.apps-other",
    title: "Account: connect steps, other AI apps",
    url: "/account#apps",
    api: acct({ [CONNS]: { connections: [] } }),
    steps: [{ click: "text=Other AI apps" }],
  },
  { id: "account.apps-disconnect", title: "Account: disconnect an app, asked first", url: "/account#apps", api: acct(), steps: [{ click: "text=Disconnect" }] },
  { id: "account.emails-on", title: "Account: email preferences on", url: "/account#emails", api: acct() },
  { id: "account.emails-off", title: "Account: email preferences off", url: "/account#emails", api: acct({ "/v1/me": { ...ME, signin_alerts: false }, [PREFS]: { email_updates: false, email_possible: true } }) },
  { id: "account.emails-key-only", title: "Account: email preferences, no address", url: "/account#emails", api: acct({ "/v1/me": KEY_ONLY, [PREFS]: { email_updates: true, email_possible: false } }) },
  {
    id: "account.emails-saved",
    title: "Account: a preference saved, with Undo",
    url: "/account#emails",
    api: acct({ "PATCH /v1/hack/preferences": { email_updates: false, email_possible: true } }),
    steps: [{ click: "input[type=checkbox]@1" }],
  },

  // Create event
  { id: "organiser-create-event.default", title: "Create event: empty form", url: "/events/new", api: api() },
  {
    id: "organiser-create-event.filled",
    title: "Create event: filled in, the address is free",
    url: "/events/new",
    api: api({ "/v1/hack/names/generation-girl-2026": { available: true } }),
    steps: [
      { fill: ["#new-title", "Generation Girl 2026"] },
      { fill: ["#new-start", "2026-10-28"] },
      { fill: ["#new-end", "2026-10-29"] },
      { fill: ["#new-organiser", "Amara Wijaya"] },
      { fill: ["#new-org", "Generation Girl"] },
      { fill: ["#new-email", "team@generationgirl.org"] },
      { fill: ["#new-n", "80"] },
    ],
  },
  {
    id: "organiser-create-event.slug-taken",
    title: "Create event: the address is taken",
    url: "/events/new",
    api: api({ "/v1/hack/names/green-campus": { available: false, code: "name_taken" } }),
    steps: [
      { fill: ["#new-title", "Green Campus"] },
      { fill: ["#new-organiser", "Amara Wijaya"] },
      { fill: ["#new-email", "team@generationgirl.org"] },
      { fill: ["#new-start", "2026-10-28"] },
      { fill: ["#new-org", "Generation Girl"] },
      { fill: ["#new-end", "2026-10-29"] },
      { fill: ["#new-n", "80"] },
    ],
  },
  { id: "organiser-create-event.invalid", title: "Create event: submitted empty, each field says what it needs", url: "/events/new", api: api(), steps: [{ fill: ["#new-n", "0"] }, { click: "text=Create event@1" }] },
  {
    id: "organiser-create-event.limit",
    title: "Create event: refused, the person already runs the most events allowed",
    url: "/events/new",
    api: api({
      "/v1/hack/names/kopi-fest": { available: true },
      "POST /v1/hack/events": refuse(409, "too_many_active_events", "You already have 3 events, the most this site allows. Events count until they're ended: end one from its Home, or delete it in Settings."),
    }),
    steps: [
      { fill: ["#new-title", "Kopi Fest"] },
      { fill: ["#new-start", "2026-12-05"] },
      { fill: ["#new-organiser", "Amara Wijaya"] },
      { fill: ["#new-org", "Generation Girl"] },
      { fill: ["#new-email", "team@generationgirl.org"] },
      { fill: ["#new-purpose", "A weekend for coffee farmers' tools."] },
      { fill: ["#new-n", "80"] },
      { click: "text=Create event@1" },
    ],
  },
  {
    id: "organiser-create-event.leave-unsaved",
    title: "Create event: leaving with edits asks first",
    url: "/events/new",
    api: api(),
    steps: [{ fill: ["#new-title", "Kopi Fest"] }, { click: "text=Your events" }],
  },
];
