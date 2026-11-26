// The link pages' plain logic (join, apply, judge, organiser and team links):
// the refusal words they show, the dates in their headers and the answers
// their forms send. Ported from the old screens/public-*.js and the ui.js
// helpers they used (ERROR_MESSAGES, errText, countryName; zone labels are lib/time's).
import type { ApiError } from "@kit/api/call";
import { dateTime, isLocalMidnight, zonedDate, zonedParts } from "@/lib/time";

/** The shared refusal words (ui.js ERROR_MESSAGES) the link pages can meet. */
export const ERRORS: Record<string, string> = {
  coc_required: "Please agree to the code of conduct.",
  invalid_display_name: "Enter your name on one line, without special formatting.",
  already_member: "You are already in this event.",
  admin_cannot_join: "The platform admin cannot join an event.",
  judging_closed: "Judging is closed for this event.",
  event_taken_down: "This event has been taken down.",
  invalid_code: "This link does not work any more.",
  event_closed: "This event has ended.",
  invalid_request: "Something in that request wasn't right. Reload and try again.",
  rate_limited: "Too many changes at once. Wait a moment and try again.",
  already_in_team: "You are already on a team.",
  team_full: "That team is full.",
  teams_locked: "Teams are fixed now.",
  joining_closed: "Sign-up has closed for this event.",
  answer_required: "Answer every required question.",
  invalid_answers: "Something in that request wasn't right. Reload and try again.",
  team_locked: "This team isn't taking new people.",
  team_kept: "This team can't take new members.",
  team_link_not_found: "That link or code doesn't match a team in this event.",
};

/** Joining and team links are one-off attempts, so a rate limit reads "tries". */
export const JOIN_ERRORS: Record<string, string> = { ...ERRORS, rate_limited: "Too many tries at once. Wait a moment and try again." };

export const APPLY_ERRORS: Record<string, string> = {
  ...ERRORS,
  rate_limited: "Too many applications from this account. Wait a while and try again.",
  applications_closed: "This event isn't taking applications.",
  too_many_pending: "You have 20 applications waiting. Wait for answers before applying to more.",
  event_applications_full: "This event has as many people waiting as it can take right now. Try again once the organisers have answered some of them.",
};

export const OFFLINE_LOAD = "Cannot reach the server. Try the link again in a moment.";

/** errText is the line a refusal shows: this page's words for its code, else the server's own. */
export function errText(e: unknown, codes: Record<string, string> = ERRORS, fallback = "Something went wrong."): string {
  const err = e as Partial<ApiError>;
  if (err?.code && codes[err.code]) return codes[err.code];
  if (err?.status === 0) return "Cannot reach the server.";
  return err?.message || fallback;
}

export const isOffline = (e: unknown) => (e as ApiError)?.status === 0;
export const isGone = (e: unknown, code: string) => (e as ApiError)?.status === 404 || (e as ApiError)?.code === code;

/** accountName is the one name a person has across events (owner, 2026-11-06): the invite's, else the account's. */
export function accountName(fromInvite: unknown, me?: { display_name?: string } | null): string {
  return (typeof fromInvite === "string" ? fromInvite : me?.display_name || "").trim();
}

/** checkJoin is the form's own check before it sends: the code of conduct, then the name when one is asked for. */
export function checkJoin(o: { coc: boolean; askName: boolean; name: string }): { coc?: string; name?: string } {
  const out: { coc?: string; name?: string } = {};
  if (!o.coc) out.coc = ERRORS.coc_required;
  if (o.askName && !o.name.trim()) out.name = "Enter your name.";
  return out;
}

/** unanswered is the required questions left empty, each with its line. */
export function unanswered(questions: Question[], answers: Record<string, string>): Record<string, string> {
  const out: Record<string, string> = {};
  for (const q of questions) if (q.required && !(answers[q.id] || "").trim()) out[q.id] = "Answer this question.";
  return out;
}

/** joinBody is what accepting a link sends: display_name only when the account has no name yet. */
export function joinBody(o: { askName: boolean; name: string; answers?: Record<string, string> }): Record<string, unknown> {
  const body: Record<string, unknown> = { accept_coc: true };
  if (o.answers) body.answers = o.answers;
  if (o.askName) body.display_name = o.name.trim();
  return body;
}

/** closedLine is why a join or judge link is not open (its stage). */
export function closedLine(stage: string | undefined, kind: "join" | "judge"): string {
  if (stage === "draft") return "This event is not open yet.";
  if (stage === "archived") return "This event is over.";
  return kind === "join" ? "Sign-up has closed for this event." : "This judge invite isn’t open any more.";
}

/** memberLine is what a join link says to someone already in its event. */
export function memberLine(approval: string | undefined, title: string): { text: string; tone: "info" | "bad"; open: "page" | "team" } {
  if (approval === "rejected") return { text: "Your application wasn't accepted this time.", tone: "bad", open: "page" };
  if (approval === "pending") return { text: "Your application is waiting for an organiser to approve it. This usually takes a day or two.", tone: "info", open: "page" };
  return { text: `You're already in ${title}.`, tone: "info", open: "team" };
}


/** judgeDates is the judge invite's "start – end" (public-judge.js dateRange). */
export function judgeDates(info: { starts_at?: string | null; ends_at?: string | null; time_zone?: string }): string {
  if (!info.starts_at) return "";
  const at = (v: string) => zonedDate(v, info.time_zone, { dateOnly: isLocalMidnight(v, info.time_zone) });
  const start = at(info.starts_at);
  return info.ends_at ? `${start} – ${at(info.ends_at)}` : start;
}

const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

/** applyDates is the apply header's plain range ("28–29 Oct 2026"), with times when a boundary has one. */
export function applyDates(startsAt?: string | null, endsAt?: string | null, zone?: string): string {
  if ((startsAt && !isLocalMidnight(startsAt, zone)) || (endsAt && !isLocalMidnight(endsAt, zone)))
    return [startsAt, endsAt]
      .filter(Boolean)
      .map((v) => dateTime(v, zone, { dateOnly: isLocalMidnight(v!, zone) }))
      .join(" – ");
  const part = (v?: string | null) => {
    const p = zonedParts(v, zone);
    return p ? { day: String(Number(p.d)), month: MONTHS[Number(p.m) - 1], year: p.y } : null;
  };
  const a = part(startsAt);
  const b = part(endsAt);
  if (!a && !b) return "";
  if (a && !b) return `${a.day} ${a.month} ${a.year}`;
  if (b && !a) return `${b.day} ${b.month} ${b.year}`;
  if (a!.year === b!.year && a!.month === b!.month && a!.day === b!.day) return `${a!.day} ${a!.month} ${a!.year}`;
  if (a!.year === b!.year && a!.month === b!.month) return `${a!.day}–${b!.day} ${a!.month} ${a!.year}`;
  if (a!.year === b!.year) return `${a!.day} ${a!.month}–${b!.day} ${b!.month} ${a!.year}`;
  return `${a!.day} ${a!.month} ${a!.year}–${b!.day} ${b!.month} ${b!.year}`;
}

// Every country the server accepts for an applicant (the fixed ISO 3166-1
// alpha-2 list of public-apply.js, 250 codes; XK kept, historical codes left out).
const COUNTRY_CODES =
  "AD AE AF AG AI AL AM AO AQ AR AS AT AU AW AX AZ BA BB BD BE BF BG BH BI BJ BL BM BN BO BQ BR BS BT BV BW BY BZ CA CC CD CF CG CH CI CK CL CM CN CO CR CU CV CW CX CY CZ DE DJ DK DM DO DZ EC EE EG EH ER ES ET FI FJ FK FM FO FR GA GB GD GE GF GG GH GI GL GM GN GP GQ GR GS GT GU GW GY HK HM HN HR HT HU ID IE IL IM IN IO IQ IR IS IT JE JM JO JP KE KG KH KI KM KN KP KR KW KY KZ LA LB LC LI LK LR LS LT LU LV LY MA MC MD ME MF MG MH MK ML MM MN MO MP MQ MR MS MT MU MV MW MX MY MZ NA NC NE NF NG NI NL NO NP NR NU NZ OM PA PE PF PG PH PK PL PM PN PR PS PT PW PY QA RE RO RS RU RW SA SB SC SD SE SG SH SI SJ SK SL SM SN SO SR SS ST SV SX SY SZ TC TD TF TG TH TJ TK TL TM TN TO TR TT TV TW TZ UA UG UM US UY UZ VA VC VE VG VI VN VU WF WS XK YE YT ZA ZM ZW".split(
    " ",
  );

/** countryName is a code in the browser's own language, the bare code when that fails. */
export function countryName(code: string): string {
  for (const lang of [navigator.language, "en"])
    try {
      return new Intl.DisplayNames([lang], { type: "region" }).of(code) || code;
    } catch {
      /* a locale the engine refuses: try the next */
    }
  return code;
}

export const countries = () => COUNTRY_CODES.map((code) => ({ code, name: countryName(code) })).sort((a, b) => a.name.localeCompare(b.name));
export const COUNTRY_COUNT = COUNTRY_CODES.length;

export type Option = { value: string; label: string };
export const AGE_RANGES: Option[] = [
  { value: "under_18", label: "Under 18" },
  { value: "18_24", label: "18-24" },
  { value: "25_34", label: "25-34" },
  { value: "35_plus", label: "35+" },
];
export const GENDERS: Option[] = [
  { value: "woman", label: "Woman or girl" },
  { value: "man", label: "Man or boy" },
  { value: "non_binary", label: "Non-binary" },
  { value: "another", label: "Another gender" },
  { value: "prefer_not_to_say", label: "Prefer not to say" },
];

/** applyField is the form field a server refusal belongs under, or "" for the form's own line. */
export function applyField(code: string | undefined): string {
  return (
    {
      coc_required: "coc",
      invalid_display_name: "name",
      invalid_city: "city",
      invalid_country: "country",
      invalid_age_range: "age",
      invalid_gender: "gender",
    } as Record<string, string>
  )[code || ""] || "";
}

/** article is "a" or "an" before a role ("an organiser"). */
export const article = (word: string) => (/^[aeiou]/i.test(word) ? "an" : "a");

// The payloads (typed here; the client's invites/people functions return them untyped).
export type Question = { id: string; prompt: string; required?: boolean };
export type JoinInfo = {
  slug: string;
  title?: string;
  organiser_name?: string;
  organisation?: string;
  joinable?: boolean;
  stage?: string;
  approval_required?: boolean;
  signup_questions?: Question[];
  coc_default?: string;
  coc_text?: string;
  account_name?: string;
};
export type JudgeInfo = JoinInfo & { tagline?: string; starts_at?: string | null; ends_at?: string | null; time_zone?: string };
export type OrganiserInvite = { title: string; joinable?: boolean; organiser_name?: string; coc_default?: string; coc_text?: string; account_name?: string };
export type ApplyInfo = {
  title?: string;
  organiser_name?: string;
  organisation?: string;
  starts_at?: string | null;
  ends_at?: string | null;
  time_zone?: string;
  applications_open?: boolean;
  closed_reason?: string;
  me?: { role?: string; approval_status?: string } | null;
  fields?: { age_range?: Option[]; gender?: Option[] };
  signup_questions?: Question[];
  coc_default?: string;
  coc_text?: string;
  account_name?: string;
};
export type TeamLinkInfo = {
  event?: { slug?: string; title?: string; applications_open?: boolean };
  team?: { name?: string; member_count?: number; team_size_max?: number; track?: { name?: string } | null };
  you?: { status?: string; team_name?: string } | null;
  reason?: string;
};

/** teamLinkCase is which answer the team link gives this person, in the old screen's order. */
export function teamLinkCase(info: TeamLinkInfo, signedIn: boolean): string {
  if (!signedIn) return "signed-out";
  const status = info.you?.status;
  if (status === "organiser" || status === "judge") return "staff";
  if (status && ["pending", "rejected", "not_in_event", "on_this_team", "on_other_team"].includes(status)) return status;
  if (info.reason && ["locked", "full", "teams_closed"].includes(info.reason)) return info.reason;
  return "ready";
}
