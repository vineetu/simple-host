// The judge's Overview (/e/{slug}/judge?s=home): is scoring open, and the one
// next thing to do. Needs you, a pinned announcement, the next-step card
// (or the stage's line in its place), the checklist, then Leave this event.
// The walkthrough opens itself the first time (judge_walkthrough_done).
import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { Button } from "@kit/components/ui/button";
import { PageTitle, Section } from "@kit/components/ui/page-shell";
import { Skeleton } from "@kit/components/ui/skeleton";
import { useHack, type EventDetail } from "@/lib/client";
import { href } from "@/routes/resolve";
import { useEventDetail } from "@/app/data";
import { useLeave } from "@/app/Leave";
import { AppLink } from "@/app/nav";
import { Failed } from "@/app/states";
import type { ScreenProps } from "@/routes/registry";
import { homeTop, leaveLine, setupStep, teamWord, type HomeTop } from "./data";
import { NeedsYou } from "@/shared/NeedsYou";
import { JudgeChecklist } from "./Checklist";
import { Box } from "@/shared/Box";
import { NotOpen, Progress } from "./parts";
import { useJudgeChecklist, useMyConflicts, useQueue } from "./queries";
import { homeSteps, useTour } from "./tours";

const scoreHref = (slug: string, team: string) => href("judge", slug, "scoring") + "&team=" + encodeURIComponent(team);

function Pinned({ slug }: { slug: string }) {
  const api = useHack();
  // Left out quietly when it does not load.
  const q = useQuery({ queryKey: ["announcements", slug], queryFn: () => api.messages.announcements(slug) });
  const pinned = ((q.data?.announcements as { title: string; body_html?: string; pinned?: boolean }[] | undefined) || []).find((a) => a.pinned);
  if (!pinned) return null;
  return (
    <Box>
      <h2 className="text-sm text-ink-soft">Pinned announcement</h2>
      <h3 className="mt-1 type-section">{pinned.title}</h3>
      {/* The server writes this body from the organiser's text, sanitised (the old screen showed it the same way). */}
      <div className="mt-1 [&_a]:text-accent [&_p]:mt-2" dangerouslySetInnerHTML={{ __html: pinned.body_html || "" }} />
    </Box>
  );
}

function Hero({ top, slug, step }: { top: HomeTop; slug: string; step: ReturnType<typeof setupStep> }) {
  const bar = (done: number, total: number, tour = true) => <Progress done={done} total={total} label={teamWord(total) + " scored"} tour={tour} />;
  switch (top.kind) {
    case "results":
      return (
        <Box tone="ok">
          <p className="type-section">Results are out.</p>
          <p className="mt-1">
            <a href={"/e/" + encodeURIComponent(slug)}>See results on the event page</a>
          </p>
        </Box>
      );
    case "not-open":
      return <NotOpen slug={slug} />;
    case "nothing":
      return (
        <Box>
          <p>Nothing assigned to you yet. Check back once the organiser assigns judges.</p>
        </Box>
      );
    case "all-conflicted":
      return (
        <Box>
          <p>You’ve declared a conflict with everyone assigned to you. There’s nothing left for you to score, by design.</p>
        </Box>
      );
    case "locked-done":
    case "locked-partial":
      return (
        <Box tone="warn">
          {top.kind === "locked-done" ? (
            <p>
              <span className="font-semibold">Scoring is locked.</span> You scored every team. Results are not out yet.
            </p>
          ) : (
            <p>
              <span className="font-semibold">Scoring was locked before you finished.</span> {top.left} {teamWord(top.left)} you were assigned {top.left === 1 ? "is" : "are"} now closed to
              scoring. The organiser finished judging, so this isn’t an error.
            </p>
          )}
          <p className="mt-3 text-sm text-ink-soft">
            {top.scored} of {top.total} {teamWord(top.total)} scored
          </p>
          {bar(top.scored, top.total)}
        </Box>
      );
  }
  // The next-step card: a setup step first (read the rubric, check who you know), else scoring.
  if (step)
    return (
      <Box tone="accent" data-tour="next" className="flex flex-wrap items-center gap-x-6 gap-y-3">
        <div className="min-w-0 flex-[1_1_18rem]">
          <p className="text-sm text-accent">Your next step</p>
          <h2 className="type-section">{step.label}</h2>
          {step.detail && <p className="mt-1 text-ink-soft">{step.detail}</p>}
        </div>
        <Button variant="primary" asChild>
          <AppLink to={step.href}>{{ "j.rubric": "Read", "j.conflicts": "Check", "j.scores": "Score" }[step.id] || "Open"}</AppLink>
        </Button>
      </Box>
    );
  const done = top.kind === "done";
  return (
    <Box tone="accent" data-tour="next" className="flex flex-wrap items-center gap-x-6 gap-y-3">
      <div className="min-w-0 flex-[1_1_18rem]">
        <p className="text-sm text-accent">{done ? "All done" : "Your next step"}</p>
        <h2 className="type-section">{done ? "You are done. Thank you." : "Scoring is open"}</h2>
        <p className="mt-1">
          {top.scored} of {top.total} {teamWord(top.total)} scored.
        </p>
        {bar(top.scored, top.total)}
        <p className="text-sm text-ink-soft">{done ? "Until the organiser finishes judging, you can change any score." : "Scoring closes when the organiser finishes judging."}</p>
      </div>
      {done ? (
        <Button asChild>
          <AppLink to={href("judge", slug, "queue")}>Review my scores</AppLink>
        </Button>
      ) : (
        <Button variant="primary" asChild>
          <AppLink to={scoreHref(slug, top.team)}>Score next</AppLink>
        </Button>
      )}
    </Box>
  );
}

function LeaveZone({ d }: { d: EventDetail }) {
  const api = useHack();
  const [openLeave, dialog] = useLeave();
  // Never a Leave button whose state is unknown: left out when the preview does not load.
  const q = useQuery({ queryKey: ["leave-preview", d.event.slug], queryFn: () => api.event.leavePreview(d.event.slug) });
  if (!q.data) return null;
  const line = leaveLine(q.data as Parameters<typeof leaveLine>[0], d.role === "organiser");
  return (
    <Section title="Leave this event" className="rounded-wobble border-[1.5px] border-bad bg-card px-4 py-3">
      <div className="flex flex-wrap items-center gap-x-6 gap-y-3">
        <div className="min-w-0 flex-[1_1_18rem]">
          <p>Leave {d.event.title}</p>
          <p className="text-sm text-ink-soft">{line.text}</p>
        </div>
        <Button variant="danger" disabled={line.disabled} onClick={() => openLeave(d.event.slug, d.event.title)}>
          Leave event
        </Button>
      </div>
      {dialog}
    </Section>
  );
}

export default function Home({ match }: ScreenProps) {
  const slug = match.slug!;
  const d = useEventDetail(slug).data!;
  const queue = useQueue(slug);
  const conflicts = useMyConflicts(slug);
  const checklist = useJudgeChecklist(slug);
  const ev = d.event;
  const top = queue.data ? homeTop(ev, queue.data) : null;
  const heroKind = top?.kind === "next" ? "next" : top?.kind === "done" ? "done" : "other";
  const step = top && (top.kind === "next" || top.kind === "done") ? setupStep(checklist.data) : null;
  const steps = useMemo(() => homeSteps(ev, step ? "other" : heroKind), [ev, heroKind, step]);
  const tour = useTour("judge", steps, !!queue.data && !checklist.isPending, true);
  const title = <PageTitle>Overview</PageTitle>;

  if (queue.isPending || conflicts.isPending)
    return (
      <>
        {title}
        <div className="mt-5" role="status" aria-busy="true" aria-label="Loading your judging">
          <Skeleton className="h-36 w-full" />
          <Skeleton className="mt-4 h-48 w-full" />
        </div>
      </>
    );
  if (queue.isError || conflicts.isError)
    return (
      <>
        {title}
        <NeedsYou role="judge" slug={d.event.slug} items={d.needs_you || []} />
        <Failed error={queue.error || conflicts.error} retry={() => (queue.refetch(), conflicts.refetch())} />
      </>
    );
  return (
    <>
      {title}
      <NeedsYou role="judge" slug={d.event.slug} items={d.needs_you || []} />
      <Pinned slug={slug} />
      <Hero top={top!} slug={slug} step={step} />
      {checklist.data && <JudgeChecklist list={checklist.data} primary={top!.kind !== "next" && top!.kind !== "done"} />}
      <LeaveZone d={d} />
      {tour}
    </>
  );
}
