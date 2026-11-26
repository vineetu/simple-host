// Event: the event record, stage, link codes, leaving, checklist, badges, reminders.
import type { Call } from "@kit/api/call";
import { E, seg, type Untyped } from "./_e";
import type { Need, Role, Stage } from "./hack";

/** GET E: the event as the signed-in member sees it. */
export type EventDetail = {
  event: {
    slug: string;
    title: string;
    stage: Stage;
    time_zone?: string;
    color?: string;
    taken_down?: boolean;
    results_published?: boolean;
    announcement_times?: string[];
    starts_at?: string | null;
    ends_at?: string | null;
    submission_deadline?: string | null;
    judging_ends_at?: string | null;
    scoring_open?: boolean;
    judging_locked_at?: string | null;
  } & Untyped;
  role: Role;
  can?: Record<string, boolean>;
  me?: {
    approval_status?: "approved" | "pending" | "rejected";
    team?: { slug: string; name?: string; admin?: boolean; pending_requests?: number } | null;
  } & Untyped;
  needs_you?: Need[];
  organiser?: { counts?: { on_no_team?: number } } & Untyped;
  admin_view?: boolean;
} & Untyped;

/** GET E/leave: what leaving would do for this person (leave.js). */
export type LeavePreview = {
  case?: "participant" | "applicant" | "judge" | "co_organiser" | "creator" | "last_organiser";
  slug?: string;
  blocked?: { message?: string } | null;
  approval_status?: string;
  rejoin?: string;
  scored_teams?: number;
  team?: { name: string; frozen?: boolean; deleted_on_leave?: boolean; others?: string[]; others_count?: number } | null;
  organisers_staying?: string[];
  successors?: { user_id: string; display_name: string; email?: string }[];
  close_options?: { end?: { blocked?: boolean } };
};
export type LeaveDone = { event?: string; successor?: { display_name: string } } & Untyped;

export const get = (call: Call, slug: string) => call<EventDetail>("GET", E(slug));
export const update = (call: Call, slug: string, body: Untyped) => call<Untyped>("PATCH", E(slug), body);
export const remove = (call: Call, slug: string, body: Untyped = {}) => call<Untyped>("DELETE", E(slug), body);
export const publicView = (call: Call, slug: string) => call<{ title: string } & Untyped>("GET", E(slug, "public"));
export const setStage = (call: Call, slug: string, stage: string, body: Untyped = {}) => call<Untyped>("POST", E(slug, "stage"), { stage, ...body });
export const newCode = (call: Call, slug: string, kind: "join" | "judge") => call<Untyped>("POST", E(slug, "codes", kind));
export const leavePreview = (call: Call, slug: string) => call<LeavePreview>("GET", E(slug, "leave"));
export const leave = (call: Call, slug: string, body: Untyped) => call<LeaveDone>("POST", E(slug, "leave"), body);
export const checklist = (call: Call, slug: string, role?: string) => call<Untyped>("GET", E(slug, "checklist") + (role ? "?role=" + seg(role) : ""));
export const checklistNotNeeded = (call: Call, slug: string, id: string) => call<Untyped>("POST", E(slug, "checklist", "not-needed", seg(id)));
export const checklistNeeded = (call: Call, slug: string, id: string) => call<Untyped>("DELETE", E(slug, "checklist", "not-needed", seg(id)));
export const acknowledge = (call: Call, slug: string, id: string) => call<Untyped>("POST", E(slug, "checklist", "acknowledgements", seg(id)));
export const noticesSeen = (call: Call, slug: string, ids: string[]) => call<Untyped>("POST", E(slug, "notices", "seen"), { ids });
export const reminders = (call: Call, slug: string) => call<Untyped>("GET", E(slug, "reminders"));
export const setReminders = (call: Call, slug: string, body: Untyped) => call<Untyped>("PUT", E(slug, "reminders"), body);
