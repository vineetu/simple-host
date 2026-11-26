// Pieces Home and Schedule share: the live countdown, the schedule nudge line
// and an announcement as the organisers wrote it.
import { useEffect, useState } from "react";
import { Badge } from "@kit/components/ui/badge";
import { cn } from "@kit/lib/utils";
import { dateTime } from "@/lib/time";
import { AppLink } from "@/app/nav";
import { countdownText, nudgeLine, type Announcement, type Row } from "./data";

const HOUR = 3600000;

/** useNow is the time, read again every `every` ms while the screen is up. */
export function useNow(every: number) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), every);
    return () => clearInterval(t);
  }, [every]);
  return now;
}

/** left is the time to a deadline, corrected by the server's clock when it sent one. */
export function useLeft(deadline: string, serverNow?: string | null) {
  const [skew] = useState(() => (serverNow ? new Date(serverNow).getTime() - Date.now() : 0));
  const target = new Date(deadline).getTime();
  const [now, setNow] = useState(() => Date.now());
  const left = Number.isNaN(target) ? 0 : target - (now + skew);
  useEffect(() => {
    if (!(left > 0)) return;
    const t = setTimeout(() => setNow(Date.now()), left <= HOUR ? 1000 : 60000);
    return () => clearTimeout(t);
  }, [left]);
  return left;
}

/** Countdown is "2 days left": red under an hour, unless quiet (already submitted). */
export function Countdown({ left, label, quiet }: { left: number; label: string; quiet?: boolean }) {
  const urgent = !quiet && left > 0 && left <= HOUR;
  return (
    <span className={cn("whitespace-nowrap", urgent ? "text-bad" : "text-ink")} role="timer" aria-live="off">
      <span className="type-section">{countdownText(left)}</span> <span className="text-sm">{label}</span>
    </span>
  );
}
export const urgentLeft = (left: number) => left > 0 && left <= HOUR;

/** Nudge is the one line about a schedule row starting soon or on now, kept current every 30 s. */
export function Nudge({ rows, ev, scheduleHref }: { rows: Row[]; ev: { time_zone?: string }; scheduleHref?: string }) {
  const now = useNow(30000);
  const line = nudgeLine(rows, ev, now);
  if (!line) return null;
  return (
    <p role="status" className="mt-4 rounded-field border-[1.5px] border-accent bg-accent-soft px-4 py-3">
      <span className="font-semibold">{line[0]}</span>
      {line[1] && " " + line[1]}
      {scheduleHref && (
        <>
          {" "}
          <AppLink to={scheduleHref}>See schedule</AppLink>
        </>
      )}
    </p>
  );
}

/** AnnouncementBody is an announcement: its title, date, pin and the organisers' text (HTML the server made safe). */
export function AnnouncementBody({ a, ev, level = "h3" }: { a: Announcement; ev: { time_zone?: string }; level?: "h2" | "h3" }) {
  const H = level;
  return (
    <>
      <H className="type-section">{a.title}</H>
      <p className="mt-0.5 flex flex-wrap items-center gap-2 text-sm text-ink-soft">
        {dateTime(a.created_at, ev, { dateOnly: true })}
        {a.pinned && <Badge>Pinned</Badge>}
      </p>
      <div
        className="mt-2 max-w-[70ch] [&_img]:h-auto [&_img]:max-w-full [&_p]:mt-2 [&_ul]:mt-2 [&_ul]:list-disc [&_ul]:pl-5 [&_ol]:mt-2 [&_ol]:list-decimal [&_ol]:pl-5"
        dangerouslySetInnerHTML={{ __html: a.body_html || "" }}
      />
    </>
  );
}
