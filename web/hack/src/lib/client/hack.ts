// Hack, account level: my events, create, name check, to-dos, preferences, directory.
import type { Call } from "@kit/api/call";
import { seg, type Untyped } from "./_e";

export type Role = "organiser" | "participant" | "judge";
export type Stage = "draft" | "open" | "building" | "closed" | "judging" | "results" | "archived";

/** One row of GET /v1/hack/events. */
export type EventRow = {
  slug: string;
  title: string;
  stage: Stage;
  role: Role;
  can?: Record<string, boolean>;
  ended_at?: string | null;
  url?: string;
  icon_url?: string;
  accent_color?: string;
  color?: string;
  starts_at?: string | null;
  ends_at?: string | null;
  taken_down?: boolean;
  time_zone?: string;
  /** Participants only: their sign-up's state. */
  approval_status?: "approved" | "pending" | "rejected";
};

/** A thing waiting on the person (GET /v1/hack/todos, and an event's needs_you). */
export type Need = { id?: string; kind: string; text: string; section?: string; team?: string; count?: number; dismissable?: boolean };
export type Todos = { events: { slug: string; title: string; role: Role; items: Need[] }[] };

export const events = async (call: Call) => {
  const r = await call<EventRow[] | { events: EventRow[] }>("GET", "/v1/hack/events");
  return Array.isArray(r) ? r : r.events || [];
};
export const create = (call: Call, body: Untyped) => call<Untyped>("POST", "/v1/hack/events", body);
export const nameCheck = (call: Call, slug: string) => call<Untyped>("GET", "/v1/hack/names/" + seg(slug));
export const todos = (call: Call) => call<Todos>("GET", "/v1/hack/todos");
export const preferences = (call: Call) => call<Untyped>("GET", "/v1/hack/preferences");
export const setPreferences = (call: Call, body: Untyped) => call<Untyped>("PATCH", "/v1/hack/preferences", body);
export const directory = (call: Call) => call<Untyped>("GET", "/v1/hack/directory");
export const myTeams = (call: Call) => call<Untyped>("GET", "/v1/hack/my-teams");

// Account and Create event (screens/account).
/** GET/PATCH /v1/hack/preferences: the "email me when something waits on me" switch. */
export type Preferences = { email_updates?: boolean; email_possible?: boolean } & Untyped;
export const emailPreferences = (call: Call) => call<Preferences>("GET", "/v1/hack/preferences");
export const setEmailUpdates = (call: Call, on: boolean) => call<Preferences>("PATCH", "/v1/hack/preferences", { email_updates: on });
/** GET /v1/hack/names/{slug}: whether an event address is free. */
export const nameFree = (call: Call, slug: string) => call<{ available?: boolean; code?: string; error?: string }>("GET", "/v1/hack/names/" + seg(slug));
export type CreateEvent = {
  slug: string;
  title: string;
  organiser_name: string;
  organisation: string;
  contact_email: string;
  purpose: string;
  expected_participants: number;
  starts_at: string;
  ends_at?: string;
  time_zone: string;
};
export const createEvent = (call: Call, body: CreateEvent) => call<{ event?: { slug?: string } }>("POST", "/v1/hack/events", body);
/** The per-app connect steps (a static file shared with /get-started; read like a call so the catalogue can answer it). */
export type ConnectStep = { html: string; after?: string; address?: boolean; command?: string; copy?: { id: string; value: string; label: string } };
export type ConnectApp = { id: string; label: string; tab?: boolean; connect?: ConnectStep[]; connect_hosted?: ConnectStep[]; fallback?: string };
export const connectSteps = (call: Call) => call<{ apps?: ConnectApp[]; mcp_path?: string }>("GET", "/hack-connect-steps.json");
