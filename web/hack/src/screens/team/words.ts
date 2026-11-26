// Words shared by the team, entry and website screens (the old _partB-util.js):
// the plain sentence for a refusal, the codes that mean "the page is out of
// date, read it again", and the short time-ago line.
import { ApiError } from "@kit/api/call";

// The old ui.ERROR_MESSAGES lines these three screens can meet, with the
// team screen's own wording for team_not_found. A code listed here wins over
// the server's sentence, as the old errText did.
const CODES: Record<string, string> = {
  event_taken_down: "This event has been taken down.",
  event_closed: "This event has ended.",
  team_not_found: "That team code does not match a team.",
  invalid_request: "Something in that request wasn't right. Reload and try again.",
  rate_limited: "Too many changes at once. Wait a moment and try again.",
  team_finding_off: "Team finding is off.",
  team_action_unavailable: "This team action is not available.",
  invalid_looking_for: "Keep the looking-for note to one line, up to 120 characters.",
  invalid_intro: "Keep your intro to one line, up to 120 characters.",
  invalid_title: "The title must be one line, up to 80 characters.",
  invalid_tagline: "The tagline must be one line, up to 160 characters.",
  invalid_description: "The description is too long or has characters we can't keep.",
  invalid_team_name: "Give the team a name of up to 80 characters, on one line.",
  invalid_track: "Choose a listed track.",
  already_in_team: "You are already on a team.",
  team_full: "That team is full.",
  team_name_taken: "There is already a team with that name.",
  teams_locked: "Teams are fixed now.",
  invalid_url: "Enter a full link starting with https://",
  no_team: "Join or start a team first.",
  submissions_closed: "Entries are closed for your team. Ask the organiser if you need more time.",
  team_sites_not_ready: "Your team's address is still being set up. Try again in a few minutes.",
  image_too_large: "The screenshot must be 2 MB or smaller.",
  unsupported_image: "The screenshot must be a PNG, JPEG or WebP image.",
  team_site_taken_down: "The organiser has taken your team's site down.",
  deleted: "That no longer exists.",
  not_found: "That no longer exists.",
  invalid_events: "Choose at least one notification event.",
};

/** say is the one line to show for a refusal (the old errText with this group's codes). */
export function say(err: unknown, fallback = "Something went wrong."): string {
  if (err instanceof ApiError) {
    if (CODES[err.code]) return CODES[err.code];
    return err.message || fallback;
  }
  return (err as Error)?.message || fallback;
}

export const codeOf = (err: unknown) => (err instanceof ApiError ? err.code : "");

/** blocked is a refusal that means the team froze, was taken down or the event closed meanwhile: read the page again. */
export const blocked = (err: unknown) =>
  ["submissions_closed", "team_site_taken_down", "event_closed", "event_taken_down", "teams_locked"].includes(codeOf(err));

/** signedOut is the refusal the call already handled (it went to sign-in): say nothing. */
export const signedOut = (err: unknown) => err instanceof ApiError && err.status === 401;

/** ago: "just now", "4 minutes ago", "2 days ago"; "" when unknown or in the future. */
export function ago(iso: string | null | undefined, now = Date.now()): string {
  if (!iso) return "";
  const diff = now - new Date(iso).getTime();
  if (!Number.isFinite(diff) || diff < 0) return "";
  if (diff < 60000) return "just now";
  const m = Math.floor(diff / 60000);
  if (m < 60) return m + " minute" + (m === 1 ? "" : "s") + " ago";
  const h = Math.floor(m / 60);
  if (h < 24) return h + " hour" + (h === 1 ? "" : "s") + " ago";
  const d = Math.floor(h / 24);
  return d + " day" + (d === 1 ? "" : "s") + " ago";
}

export const ucfirst = (s: string) => (s ? s[0].toUpperCase() + s.slice(1) : s);
