// Overview (/e/{slug}/team?s=home): the participant's event home. What waits
// on them (Needs you, each news line with Dismiss), a schedule row starting
// soon, "Your next step" (one plain line for where they and the event are),
// the four steps of "Your entry" with a live countdown and Submit, the way to
// the rubric, the latest announcements, and Leave this event at the foot.
// The walkthrough opens itself here once.
import { useState } from "react";
import { useQueries, useQueryClient } from "@tanstack/react-query";
import { Button } from "@kit/components/ui/button";
import { AskDialog } from "@kit/components/ui/dialog";
import { Notice } from "@kit/components/ui/notice";
import { PageTitle, Section } from "@kit/components/ui/page-shell";
import { Skeleton } from "@kit/components/ui/skeleton";
import { useToast } from "@kit/components/ui/toast";
import { cn } from "@kit/lib/utils";
import type { ApiError } from "@kit/api/call";
import { useHack, type LeavePreview, type Need } from "@/lib/client";
import { dateTime } from "@/lib/time";
import { href, needHref } from "@/routes/resolve";
import { useEventDetail, useMe } from "@/app/data";
import { participantWarning, useLeave } from "@/app/Leave";
import { AppLink } from "@/app/nav";
import type { ScreenProps } from "@/routes/registry";
import {
  entrySection,
  hero,
  justPlaced,
  merge,
  needsFor,
  splitNeed,
  type Announcement,
  type Content,
  type EntryRow,
  type EntrySection,
  type Hero,
  type HomeDetail,
  type OpenTeams,
} from "./data";
import { AnnouncementBody, Countdown, Nudge, urgentLeft, useLeft } from "./parts";
import { useWalkthrough } from "./walkthrough";

const box = "mt-6 rounded-wobble border border-rule bg-card px-4 py-4 md:px-5";

function Go({ to, external, primary, children }: { to: string; external?: boolean; primary?: boolean; children: string }) {
  return (
    <Button variant={primary ? "primary" : "secondary"} asChild>
      {external ? (
        <a href={to} target="_blank" rel="noopener noreferrer">
          {children} <span aria-hidden="true">↗</span>
          <span className="sr-only"> (opens in a new tab)</span>
        </a>
      ) : (
        <AppLink to={to}>{children}</AppLink>
      )}
    </Button>
  );
}

function NeedsYou({ slug, items }: { slug: string; items: Need[] }) {
  const api = useHack();
  const qc = useQueryClient();
  const toast = useToast();
  const [busy, setBusy] = useState("");
  if (!items.length) return null;
  const dismiss = async (id: string) => {
    setBusy(id);
    try {
      await api.event.noticesSeen(slug, [id]);
      await qc.invalidateQueries({ queryKey: ["event", slug] });
    } catch (e) {
      if ((e as ApiError).status !== 401) toast((e as Error).message || "Could not dismiss that. Try again.", { bad: true });
    } finally {
      setBusy("");
    }
  };
  return (
    <Section title="Needs you">
      <ul aria-label="Needs you" className="m-0 list-none rounded-wobble border border-rule bg-card px-3 py-1">
        {items.map((item, i) => {
          const [title, sub] = splitNeed(item);
          return (
            <li key={item.id || i} className="flex min-h-14 items-center gap-2 border-b border-rule last:border-b-0">
              <AppLink to={needHref("participant", slug, item)} className="flex min-h-11 min-w-0 flex-1 flex-col justify-center px-1 py-2 no-underline">
                <span className="font-semibold text-ink">{title}</span>
                {sub && <span className="text-sm text-ink-soft">{sub}</span>}
              </AppLink>
              {item.dismissable && item.id && (
                <Button aria-label={"Dismiss: " + title} disabled={busy === item.id} onClick={() => dismiss(item.id!)}>
                  Dismiss
                </Button>
              )}
            </li>
          );
        })}
      </ul>
    </Section>
  );
}

function NextStep({ h, urgent }: { h: Hero; urgent: boolean }) {
  return (
    <section
      data-tour="next"
      aria-labelledby="next-h"
      className={cn("mt-6 flex flex-wrap items-center gap-4 rounded-wobble border-[1.5px] bg-accent-soft px-4 py-4 md:px-5", urgent ? "border-bad" : "border-accent")}
    >
      <div className="min-w-0 flex-[1_1_18rem]">
        <p className="text-sm text-ink-soft">Your next step</p>
        <h2 id="next-h" className="type-section">
          {h.title}
        </h2>
        {h.note && <p className="mt-1 text-ink-soft">{h.note}</p>}
      </div>
      {h.label && h.href && (
        <Go to={h.href} external={h.external} primary>
          {h.label}
        </Go>
      )}
    </section>
  );
}

function Tick({ row, n }: { row: EntryRow; n: number }) {
  return (
    <span
      aria-hidden="true"
      className={cn(
        "grid size-8 flex-none place-items-center rounded-full border-[1.5px] text-sm",
        row.done ? "border-good bg-good text-white" : row.current ? "border-accent text-accent" : "border-rule text-ink-soft",
      )}
    >
      {row.done ? "✓" : n}
    </span>
  );
}

function YourEntry({ s, onSubmit }: { s: EntrySection; onSubmit: () => void }) {
  return (
    <section data-tour="entry-checklist" aria-labelledby="entry-h" className={box}>
      <h2 id="entry-h" className="type-section">
        Your entry
      </h2>
      {(s.countdown || s.deadline) && (
        <p className="mt-1 flex flex-wrap items-baseline gap-x-2">
          {s.countdown && <EntryCountdown c={s.countdown} serverNow={s.serverNow} />}
          {s.deadline && <span className="text-sm text-ink-soft">{s.deadline}</span>}
        </p>
      )}
      <ol aria-label="Steps to a submitted entry" className="m-0 mt-3 list-none p-0">
        {s.rows.map((r, i) => (
          <li key={r.id} className={cn("flex flex-wrap items-center gap-3 border-b border-rule py-3 last:border-b-0")} aria-current={r.current ? "step" : undefined}>
            <Tick row={r} n={i + 1} />
            <span className="min-w-0 flex-[1_1_14rem]">
              <span className={cn("block", r.current ? "font-semibold text-ink" : r.done ? "text-ink-soft" : "text-ink")}>
                {r.label}
                {r.done && <span className="sr-only"> (done)</span>}
              </span>
              <span className="block text-sm text-ink-soft">{r.detail}</span>
            </span>
            {r.action === "submit" ? (
              <Button onClick={onSubmit}>Submit</Button>
            ) : (
              r.action && <Go to={r.action.href}>{r.action.label}</Go>
            )}
          </li>
        ))}
      </ol>
    </section>
  );
}

function EntryCountdown({ c, serverNow }: { c: NonNullable<EntrySection["countdown"]>; serverNow?: string | null }) {
  const left = useLeft(c.to, serverNow);
  return <Countdown left={left} label={c.left} quiet={c.quiet} />;
}

/** urgency is whether under an hour is left and the entry is not submitted (the next-step card turns red). */
function useUrgent(s: EntrySection | null) {
  const left = useLeft(s?.countdown?.to || "", s?.serverNow);
  return !!s?.countdown && !s.countdown.quiet && urgentLeft(left);
}

function Leave({ ev, preview }: { ev: HomeDetail["event"]; preview: LeavePreview | null }) {
  const [openLeave, dialog] = useLeave();
  if (!preview) return null;
  const b = preview.blocked as ({ code?: string; message?: string } | null | undefined);
  const text = b
    ? b.code === "event_closed"
      ? "This event has ended, so nobody can leave it. It stays on your Home as a record."
      : b.code === "event_taken_down"
        ? "This event has been taken down."
        : b.message || "You can't leave this event right now."
    : participantWarning(preview);
  return (
    <section aria-labelledby="leave-h" className="mt-10 rounded-field border-[1.5px] border-bad px-4 py-4">
      <h2 id="leave-h" className="type-section">
        Leave this event
      </h2>
      <p className="mt-1 font-semibold">Leave {ev.title}</p>
      <p className="text-sm text-ink-soft">{text}</p>
      <Button variant="danger" className="mt-3" disabled={!!b} onClick={() => openLeave(ev.slug, ev.title)}>
        Leave event
      </Button>
      {dialog}
    </section>
  );
}

const safe = <T,>(p: Promise<T>) => p.catch(() => null);

export default function Home({ match }: ScreenProps) {
  const slug = match.slug!;
  const api = useHack();
  const qc = useQueryClient();
  const toast = useToast();
  const me = useMe().data;
  const d = useEventDetail(slug).data as HomeDetail;
  const ev = d.event;
  const team = d.me?.team || null;
  const teamsOpen = (ev.stage === "open" || ev.stage === "building") && !ev.taken_down;
  const deadlinePassed = !!(ev.submission_deadline && new Date(ev.submission_deadline).getTime() <= Date.now());
  const live = ev.stage !== "archived" && !ev.taken_down;
  const wantFinding = !team && !d.me?.team_removal_notice && teamsOpen && !deadlinePassed;
  const [finding, news, leave, content] = useQueries({
    queries: [
      {
        queryKey: ["teams-open", slug],
        enabled: wantFinding,
        queryFn: () => safe(api.teams.open(slug).then((r) => ((r as OpenTeams).enabled ? (r as OpenTeams) : null))),
      },
      { queryKey: ["announcements", slug, "home"], queryFn: () => safe(api.messages.announcements(slug).then((r) => ((r.announcements as Announcement[]) || []) as Announcement[])) },
      { queryKey: ["leave-preview", slug], queryFn: () => safe(api.event.leavePreview(slug)) },
      { queryKey: ["content", slug, "home"], enabled: live, queryFn: () => safe(api.content.get(slug).then((r) => r as Content)) },
    ],
  });
  const loading = (wantFinding && finding.isPending) || news.isPending || leave.isPending || (live && content.isPending);
  const uid = me ? me.id || me.username || "anon" : "";
  // Read once per visit, as soon as the person is known (it updates this browser's baseline).
  const [placed] = useState(() => new Map<string, boolean>());
  if (uid && !placed.has(uid)) placed.set(uid, justPlaced(uid, slug, team?.slug || ""));
  const [asking, setAsking] = useState(false);
  const section = entrySection(d);
  const urgent = useUrgent(section);
  const tour = useWalkthrough(me?.id || me?.username, !loading);

  const title = <PageTitle>{ev.title}</PageTitle>;
  if (loading)
    return (
      <>
        {title}
        <div role="status" aria-busy="true" aria-label="Loading your next step">
          <Skeleton className="mt-6 h-28 w-full" />
          <Skeleton className="mt-6 h-64 w-full" />
          <Skeleton className="mt-6 h-32 w-full" />
        </div>
      </>
    );

  const h = hero(d, (finding.data as OpenTeams | null) || null);
  const rows = merge(content.data as Content | null, ev);
  const latest = ((news.data as Announcement[] | null) || []).slice(0, 2);
  const entryHref = href("participant", slug, "entry");

  const submit = async () => {
    try {
      await api.entries.submit(slug);
      toast("Submitted");
      await qc.invalidateQueries({ queryKey: ["event", slug] });
    } catch (e) {
      if ((e as ApiError).status !== 401) toast((e as Error).message || "Could not submit.", { bad: true });
    }
  };
  const until = section?.countdown?.to || (d.progress?.deadline ?? null);

  return (
    <>
      {title}
      <NeedsYou slug={slug} items={needsFor(d.needs_you, h.kind)} />
      {live && <Nudge rows={rows} ev={ev} scheduleHref={href("participant", slug, "schedule")} />}
      {placed.get(uid) && team && (
        <Notice title={`You are on ${team.name} now.`} action={<Go to={href("participant", slug, "team")}>Meet your team</Go>} />
      )}
      <NextStep h={h} urgent={urgent} />
      {section && <YourEntry s={section} onSubmit={() => setAsking(true)} />}
      <section aria-labelledby="rubric-h" className={box}>
        <h2 id="rubric-h" className="type-section">
          How you're judged
        </h2>
        <p className="mt-1 text-ink-soft">See the rubric the judges use to score your entry: criteria, descriptions and points.</p>
        <div className="mt-3">
          <Go to={entryHref}>See the rubric</Go>
        </div>
      </section>
      {latest.length > 0 && (
        <section aria-labelledby="news-h" className={box}>
          <h2 id="news-h" className="type-section">
            Latest announcements
          </h2>
          {latest.map((a) => (
            <article key={a.id} className="mt-4 border-t border-rule pt-4 first-of-type:border-t-0 first-of-type:pt-0">
              <AnnouncementBody a={a} ev={ev} />
            </article>
          ))}
          <div className="mt-4 flex flex-wrap gap-2.5">
            <Go to={href("participant", slug, "announcements")}>All announcements</Go>
            <Go to={href("participant", slug, "schedule")}>Schedule</Go>
          </div>
        </section>
      )}
      <Leave ev={ev} preview={leave.data as LeavePreview | null} />
      <AskDialog
        open={asking}
        onOpenChange={setAsking}
        title="Submit your entry"
        body={"Judges will see it. You can still change it until " + (until ? dateTime(until, ev) : "the deadline") + ". It stays submitted."}
        confirmLabel="Submit entry"
        onConfirm={submit}
      />
      {tour}
    </>
  );
}
