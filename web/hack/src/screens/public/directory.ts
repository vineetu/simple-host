// The directory's and vote page's plain words (the old hack-directory.html and
// hack-voting.html inline scripts).
import { isLocalMidnight, zonedParts } from "@/lib/time";

const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

function parts(iso: string, zone?: string) {
  const p = zonedParts(iso, zone);
  return p ? { day: String(Number(p.d)), month: MONTHS[Number(p.m) - 1], year: p.y } : null;
}

function time(iso: string, zone?: string) {
  try {
    return new Intl.DateTimeFormat("en-US", { timeZone: zone || "UTC", hour: "numeric", minute: "2-digit", hourCycle: "h12" }).format(new Date(iso)).toLowerCase();
  } catch {
    return "";
  }
}

/** dateRange is an event's dates as one short line ("3–5 Dec 2026"); a time only where it is not local midnight. */
export function dateRange(startsAt?: string | null, endsAt?: string | null, zone?: string): string {
  const a = startsAt ? parts(startsAt, zone) : null;
  const b = endsAt ? parts(endsAt, zone) : null;
  if (!a && !b) return "";
  const aTimed = !!startsAt && !isLocalMidnight(startsAt, zone);
  const bTimed = !!endsAt && !isLocalMidnight(endsAt, zone);
  const label = (p: { day: string; month: string; year: string }, iso: string, timed: boolean) => `${p.day} ${p.month} ${p.year}` + (timed ? ", " + time(iso, zone) : "");
  if (a && !b) return label(a, startsAt!, aTimed);
  if (b && !a) return label(b, endsAt!, bTimed);
  if (!aTimed && !bTimed) {
    if (a!.year === b!.year && a!.month === b!.month && a!.day === b!.day) return `${a!.day} ${a!.month} ${a!.year}`;
    if (a!.year === b!.year && a!.month === b!.month) return `${a!.day}–${b!.day} ${a!.month} ${a!.year}`;
    if (a!.year === b!.year) return `${a!.day} ${a!.month}–${b!.day} ${b!.month} ${a!.year}`;
    return `${a!.day} ${a!.month} ${a!.year}–${b!.day} ${b!.month} ${b!.year}`;
  }
  return label(a!, startsAt!, aTimed) + " – " + label(b!, endsAt!, bTimed);
}

/** ineligible is why a signed-in visitor cannot vote. */
export function ineligible(eligibility?: string): string {
  if (eligibility === "participants") return "Only approved participants can vote in this event.";
  if (eligibility === "event_members") return "Only approved event members can vote in this event.";
  return "Your account is not eligible to vote in this event.";
}

/** intro is the vote page's line under its title. */
export function intro(status: string): string {
  if (status === "open") return "Choose one team. You can change your vote until voting closes.";
  if (status === "upcoming") return "Voting opens soon.";
  if (status === "closed") return "Voting has closed. Here are the final votes.";
  return "Voting isn’t open for this event.";
}
