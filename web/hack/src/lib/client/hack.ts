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

/** One public event in GET /v1/hack/directory (now, upcoming, past). */
export type DirectoryRow = {
  slug?: string;
  title: string;
  url: string;
  tagline?: string;
  starts_at?: string | null;
  ends_at?: string | null;
  time_zone?: string;
  color?: string;
  icon_url?: string;
  applications_open?: boolean;
  apply_url?: string;
};
export type Directory = { now?: DirectoryRow[]; upcoming?: DirectoryRow[]; past?: DirectoryRow[] };
export const directoryGroups = (call: Call) => call<Directory>("GET", "/v1/hack/directory");
