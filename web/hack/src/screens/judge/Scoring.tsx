// Scoring one team (/e/{slug}/judge?s=scoring&team=…): the judge's one screen.
// Left: back to My teams and "Team n of m", the entry's summary card, then
// the video and the team's site in the event's order (judge_first), the
// code link, and the team's longer description folded. Right: progress, each
// criterion's score chips (a tap saves at once), the comment (saves by
// itself once there is a score), I know someone on this team, and Previous /
// Save & next (N and P on a keyboard). Locked: read-only.
//  - The video plays here only from the server-built embed_url on one of the
//    four fixed players, and only once Play is pressed.
//  - The site is framed only from the server-issued preview_url, on the
//    site's own host, when the server says it may be.
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useLocation, useNavigate } from "react-router";
import { useQueryClient } from "@tanstack/react-query";
import { Badge } from "@kit/components/ui/badge";
import { Button } from "@kit/components/ui/button";
import { EmptyState } from "@kit/components/ui/empty-state";
import { Label, Textarea } from "@kit/components/ui/input";
import { Skeleton } from "@kit/components/ui/skeleton";
import { useToast } from "@kit/components/ui/toast";
import { cn } from "@kit/lib/utils";
import type { ApiError } from "@kit/api/call";
import { paths, useHack, type EventDetail } from "@/lib/client";
import { safeHref } from "@/lib/labels";
import { href } from "@/routes/resolve";
import { liveScreens } from "@/routes/live";
import { useEventDetail } from "@/app/data";
import { AppLink } from "@/app/nav";
import { Failed } from "@/app/states";
import type { ScreenProps } from "@/routes/registry";
import { EntryCard } from "@/shared/EntryCard";
import {
  WHY,
  embedURL,
  eventFlags,
  framedURL,
  guardLine,
  hostLabel,
  onJudgeDocument,
  pickTeam,
  siteHref,
  walkOrder,
  weightPct,
  type Criterion,
  type Queue,
  type QueueRow,
} from "./data";
import { NewTab, NotOpen, Progress, Shot } from "./parts";
import { useMyScores, useQueue, useRefreshJudging, useRubric } from "./queries";
import { scoringSteps, useTour } from "./tours";

const teamURL = (slug: string, team: string) => href("judge", slug, "scoring") + "&team=" + encodeURIComponent(team);
/** The catalogue and vite dev have no server: frames show a stand-in there, never a real site. */
const serverMode = () => liveScreens() !== null;

function MediaBar({ title, host, open }: { title: string; host: string; open?: { to: string; label: string } }) {
  return (
    <div className="flex flex-wrap items-center gap-x-3 gap-y-1 border-b border-rule px-4 py-2">
      <h2 className="type-label">{title}</h2>
      {host && <span className="text-sm text-ink-soft">{host}</span>}
      {open && (
        <span className="ml-auto">
          <NewTab to={open.to}>{open.label}</NewTab>
        </span>
      )}
    </div>
  );
}

function Video({ row }: { row: QueueRow }) {
  const [playing, setPlaying] = useState(false);
  const v = row.video;
  if (!v || !safeHref(v.url)) return null;
  const host = v.host || hostLabel(v.url);
  const embed = embedURL(row);
  const name = row.team.name || row.title || "the team";
  return (
    <section className="mt-5 overflow-hidden rounded-wobble border border-rule bg-card" data-tour="video">
      <MediaBar title="Demo video" host={host} open={embed ? { to: safeHref(v.url), label: "Open on " + host } : undefined} />
      {embed ? (
        playing ? (
          <iframe
            title={name + " demo video"}
            src={serverMode() ? embed : undefined}
            srcDoc={serverMode() ? undefined : "<p style='font:16px system-ui;padding:16px'>The video plays here.</p>"}
            className="block aspect-video w-full border-0"
            sandbox="allow-scripts allow-same-origin allow-presentation allow-popups"
            allow="fullscreen; picture-in-picture"
            allowFullScreen
            referrerPolicy="strict-origin-when-cross-origin"
          />
        ) : (
          <button
            type="button"
            onClick={() => setPlaying(true)}
            data-play=""
            className="flex aspect-video w-full cursor-pointer flex-col items-center justify-center gap-3 bg-ink/90 text-on-accent"
          >
            <span aria-hidden="true" className="grid size-16 place-items-center rounded-tag bg-accent text-2xl">
              ▶
            </span>
            <span className="sr-only">Play the {name} demo video here. </span>
            <span className="text-sm">Plays here · nothing loads until you press play</span>
          </button>
        )
      ) : (
        <a href={safeHref(v.url)} target="_blank" rel="noopener noreferrer" className="flex min-h-16 items-center gap-4 px-4 py-3 no-underline">
          <span aria-hidden="true" className="grid size-11 flex-none place-items-center rounded-tag bg-accent text-on-accent">
            ▶
          </span>
          <span>
            <span className="block text-ink">Watch the demo video</span>
            <span className="block text-sm text-ink-soft">Opens {host} in a new tab</span>
          </span>
        </a>
      )}
    </section>
  );
}

function Site({ row, slug }: { row: QueueRow; slug: string }) {
  const site = row.site || null;
  const open = siteHref(row);
  const name = row.team.name || row.title || "Team";
  const shot = row.has_screenshot ? <Shot path={paths.teamScreenshot(slug, row.team.slug)} alt={"Screenshot of " + name} /> : null;
  if (!site && !open)
    // No site at all: the screenshot, when there is one, is the only evidence.
    return shot ? (
      <section className="mt-5 overflow-hidden rounded-wobble border border-rule bg-card">
        <MediaBar title="Screenshot" host="" />
        <div className="px-4 pb-4">{shot}</div>
      </section>
    ) : null;
  const label = row.pinned_version != null ? "Website · as submitted" : "Website";
  const host = site?.host || hostLabel(open);
  const framed = framedURL(row);
  if (framed)
    return (
      <section className="mt-5 overflow-hidden rounded-wobble border border-rule bg-card" data-tour="site">
        <MediaBar title={label} host={host} open={open ? { to: open, label: "Open" } : undefined} />
        <iframe
          title={name + " website"}
          src={serverMode() ? framed : undefined}
          srcDoc={serverMode() ? undefined : `<p style='font:16px system-ui;padding:16px'>The team’s website, framed: ${host}</p>`}
          loading="lazy"
          referrerPolicy="no-referrer"
          sandbox="allow-scripts allow-forms allow-same-origin allow-popups allow-popups-to-escape-sandbox"
          className="block h-[520px] w-full border-0 bg-white"
        />
      </section>
    );
  const why = WHY[site?.not_frameable_why || ""] || "";
  return (
    <section className="mt-5 overflow-hidden rounded-wobble border border-rule bg-card">
      <MediaBar title={label} host={host} open={open ? { to: open, label: "Open" } : undefined} />
      <div className="px-4 pb-4">
        {shot}
        {open ? (
          <>
            {why && <p className="mt-3 text-sm text-ink-soft">{why}</p>}
            <p className="mt-3" data-tour="site">
              <NewTab to={open}>Open website</NewTab>
            </p>
          </>
        ) : (
          <p className="mt-3 text-ink-soft">{site?.not_frameable_why === "taken_down" ? WHY.taken_down : "Nothing published."}</p>
        )}
      </div>
    </section>
  );
}

function Code({ row }: { row: QueueRow }) {
  const to = safeHref(row.code_url);
  if (!to) return null;
  return (
    <section className="mt-5 overflow-hidden rounded-wobble border border-rule bg-card">
      <MediaBar title="Code" host={hostLabel(to)} open={{ to, label: "Open" }} />
    </section>
  );
}

function Chips({ c, value, onPick, first }: { c: Criterion; value: number | undefined; onPick: (p: number) => void; first: boolean }) {
  const max = Number(c.max_points) || 0;
  return (
    <div role="radiogroup" aria-label={c.name} data-tour={first ? "score" : undefined} className="mt-2 flex flex-wrap gap-1.5">
      {Array.from({ length: max + 1 }, (_, p) => (
        <button
          key={p}
          type="button"
          role="radio"
          aria-checked={value === p}
          onClick={() => onPick(p)}
          className={cn(
            "grid size-11 cursor-pointer place-items-center rounded-field border-[1.5px] text-base",
            value === p ? "border-accent bg-accent text-on-accent" : "border-rule bg-card text-ink hover:border-accent",
          )}
        >
          {p}
        </button>
      ))}
    </div>
  );
}

function CritHead({ c, all }: { c: Criterion; all: Criterion[] }) {
  const pct = weightPct(c, all);
  return (
    <>
      <p className="flex items-baseline gap-2">
        <span className="font-semibold">{c.name}</span>
        {pct && <span className="text-sm text-ink-soft">{pct}</span>}
      </p>
      {c.description && <p className="text-sm text-ink-soft">{c.description}</p>}
    </>
  );
}

type Props = { d: EventDetail; queue: Queue; order: QueueRow[]; row: QueueRow; index: number; rubric: Criterion[]; locked: boolean };

function Score({ d, queue, order, row, index, rubric, locked }: Props) {
  const slug = d.event.slug;
  const api = useHack();
  const qc = useQueryClient();
  const toast = useToast();
  const navigate = useNavigate();
  const refresh = useRefreshJudging(slug);
  const saved = useMyScores(slug, row.team_id);
  const [scores, setScores] = useState<Record<string, number>>({});
  const scoresRef = useRef(scores);
  scoresRef.current = scores;
  const [comment, setComment] = useState("");
  const savedComment = useRef("");
  const [line, setLine] = useState("");
  const [guard, setGuard] = useState({ text: "", until: 0 });
  const chain = useRef<Record<string, Promise<unknown>>>({});
  const commentSave = useRef<Promise<boolean> | null>(null);
  const timer = useRef(0);
  const [seeded, setSeeded] = useState(false);

  useEffect(() => {
    if (!saved.data || seeded) return;
    const s: Record<string, number> = {};
    for (const x of saved.data.scores || []) s[x.criterion_id] = x.points;
    setScores(s);
    setComment(saved.data.comment || "");
    savedComment.current = saved.data.comment || "";
    setLine(Object.keys(s).length ? "" : "Tap a score. It saves straight away.");
    setSeeded(true);
  }, [saved.data, seeded]);

  const title = row.title || row.team.name || "Entry";
  useEffect(() => {
    document.title = `Score: ${title} · Simple Hack`;
  }, [title]);

  const hasScore = () => Object.values(scoresRef.current).some((v) => v != null);
  const commentBox = useRef<HTMLTextAreaElement>(null);

  const flushComment = useCallback(async (): Promise<boolean> => {
    clearTimeout(timer.current);
    if (commentSave.current) await commentSave.current;
    const val = commentBox.current?.value ?? savedComment.current;
    if (val === savedComment.current) return true;
    // A comment is saved with a score: notes typed before any score wait until the first tap.
    if (!hasScore()) return true;
    const p = (async () => {
      try {
        await api.judging.score(slug, row.team_id, { scores: [], comment: val });
        savedComment.current = val;
        return true;
      } catch (e) {
        if ((e as ApiError).status !== 401) toast((e as Error).message || "Cannot reach the server.", { bad: true });
        return false;
      }
    })();
    commentSave.current = p;
    try {
      return await p;
    } finally {
      if (commentSave.current === p) commentSave.current = null;
    }
  }, [api, slug, row.team_id, toast]);

  const flushScores = () => Promise.allSettled(Object.values(chain.current));

  // Each criterion's writes go one after another, so the last tap always wins on the server.
  const pick = (c: Criterion, p: number) => {
    const before = scoresRef.current[c.id];
    setScores((s) => ({ ...s, [c.id]: p }));
    scoresRef.current = { ...scoresRef.current, [c.id]: p };
    const run = () => api.judging.score(slug, row.team_id, { scores: [{ criterion_id: c.id, points: p }] }).then((r) => ({ r }), (err: unknown) => ({ err }));
    const next = (chain.current[c.id] || Promise.resolve()).then(run, run);
    chain.current[c.id] = next;
    void next.then((out) => {
      if ("err" in out) {
        // Roll back only if nothing newer was picked for this criterion since.
        if (scoresRef.current[c.id] === p) {
          setScores((s) => ({ ...s, [c.id]: before as number }));
          scoresRef.current = { ...scoresRef.current, [c.id]: before as number };
        }
        if ((out.err as ApiError).status !== 401) toast((out.err as Error).message || "Cannot reach the server. Try again.", { bad: true });
        return;
      }
      const done = !!(out.r as { complete?: boolean }).complete;
      qc.setQueryData(["judge-queue", slug], (q: Queue | undefined) => q && { ...q, queue: q.queue.map((x) => (x.team_id === row.team_id ? { ...x, done } : x)) });
      qc.setQueryData(["judge-scores", slug, row.team_id], out.r);
      if (rubric.every((k) => scoresRef.current[k.id] != null)) setGuard({ text: "", until: 0 });
      setLine("Score saved");
      if ((commentBox.current?.value ?? "") !== savedComment.current) void flushComment();
    });
  };

  const last = index >= order.length - 1;
  const go = (team: string) => navigate(teamURL(slug, team));
  const toQueue = () => navigate(href("judge", slug, "queue"));

  const next = async () => {
    const unscored = rubric.filter((c) => scoresRef.current[c.id] == null).map((c) => c.name);
    if (unscored.length && Date.now() > guard.until) {
      setGuard({ text: guardLine(unscored), until: Date.now() + 10_000 });
      return;
    }
    if (!(await flushComment())) return;
    await flushScores();
    void refresh();
    if (last) toQueue();
    else go(order[index + 1].team_id);
  };
  const prev = async () => {
    if (index <= 0) return;
    if (!locked) {
      if (!(await flushComment())) return;
      await flushScores();
    }
    go(order[index - 1].team_id);
  };

  const [conflictBusy, setConflictBusy] = useState(false);
  const conflict = async () => {
    const name = row.team.name || title;
    const scoredNow = hasScore();
    setConflictBusy(true);
    try {
      await api.judging.declareConflict(slug, { team_id: row.team_id });
    } catch (e) {
      setConflictBusy(false);
      if ((e as ApiError).status !== 401) toast((e as Error).message || "Cannot reach the server.", { bad: true });
      return;
    }
    await refresh();
    toast(scoredNow ? `${name} left your list and your score for it stops counting.` : `You won't score ${name}.`, {
      undo: {
        label: "Undo",
        run: () =>
          void api.judging
            .removeConflict(slug, row.team_id)
            .then(() => refresh())
            .then(() => go(row.team_id))
            .catch((e: Error) => toast(e.message || "Cannot reach the server.", { bad: true })),
      },
    });
    const remaining = order.filter((q) => q.team_id !== row.team_id);
    if (!remaining.length) toQueue();
    else go(remaining[Math.min(index, remaining.length - 1)].team_id);
  };

  // N = Save & next, P = Previous, outside text fields and while no dialog is open.
  const keys = useRef({ next, prev });
  keys.current = { next, prev };
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.defaultPrevented || e.altKey || e.ctrlKey || e.metaKey || e.repeat) return;
      const t = e.target as HTMLElement | null;
      if (t?.closest?.('input, textarea, select, [contenteditable=""], [contenteditable="true"]')) return;
      if (document.querySelector('[role="dialog"], [role="alertdialog"]')) return;
      const k = e.key.toLowerCase();
      if (k === "n" && !locked) {
        e.preventDefault();
        void keys.current.next();
      } else if (k === "p" && index > 0) {
        e.preventDefault();
        void keys.current.prev();
      }
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [locked, index]);

  const tour = useTour("judgeScoring", scoringSteps, seeded, !locked);
  const doneCount = (qc.getQueryData<Queue>(["judge-queue", slug]) || queue).queue.filter((q) => q.done).length;
  const judgeFirst = queue.judge_first === "site" ? "site" : "video";
  const desc = String(row.description || "").trim();
  const media =
    judgeFirst === "site" ? (
      <>
        <Site row={row} slug={slug} />
        <Code row={row} />
        <Video row={row} />
      </>
    ) : (
      <>
        <Video row={row} />
        <Site row={row} slug={slug} />
        <Code row={row} />
      </>
    );

  if (saved.isError)
    return (
      <>
        <Failed error={saved.error} retry={() => saved.refetch()} />
        <p className="text-center">
          <AppLink to={href("judge", slug, "queue")}>Back to My teams</AppLink>
        </p>
      </>
    );

  return (
    <div className="mt-4 lg:grid lg:grid-cols-[minmax(0,1fr)_minmax(320px,380px)] lg:items-start lg:gap-8">
      <h1 className="sr-only">Score: {title}</h1>
      <div className="min-w-0">
        <div className="mb-3 flex min-h-11 items-center gap-3">
          <AppLink to={href("judge", slug, "queue")} className="inline-flex min-h-11 items-center text-sm text-ink-soft no-underline hover:text-ink">
            <span aria-hidden="true">←&nbsp;</span>My teams
          </AppLink>
          <span className="ml-auto text-sm text-ink-soft">
            Team {index + 1} of {order.length}
          </span>
        </div>
        <div data-tour="card">
          <EntryCard
            entry={{
              title: row.title || row.team.name,
              team_name: row.title && row.title !== row.team.name ? row.team.name : "",
              track: row.track,
              problem: row.problem,
              tagline: row.tagline,
              audience: row.audience,
              tools: row.tools,
              maturity: row.maturity,
            }}
            badges={row.incomplete ? <Badge tone="down">Incomplete</Badge> : undefined}
          />
        </div>
        {row.incomplete && <p className="mt-2 text-sm text-ink-soft">Not complete at the deadline. Judge it as it is.</p>}
        <p className="mt-2 lg:hidden">
          <a href="#score" className="inline-flex min-h-11 items-center text-sm">
            Go to scoring ↓
          </a>
        </p>
        {media}
        {desc && (
          <details className="mt-5 rounded-wobble border border-rule bg-card px-4">
            <summary className="flex min-h-11 cursor-pointer items-center">More detail from the team</summary>
            <p className="pb-4 whitespace-pre-line">{desc}</p>
          </details>
        )}
      </div>

      <div className="mt-8 lg:sticky lg:top-4 lg:mt-0">
        <div className="flex items-center gap-3 text-sm text-ink-soft">
          <span>
            {doneCount} of {order.length} scored
          </span>
          <AppLink to={href("judge", slug, "rubric")} className="ml-auto inline-flex min-h-11 items-center">
            Rubric &amp; brief
          </AppLink>
        </div>
        <Progress done={doneCount} total={order.length} label={`${doneCount} of ${order.length} scored`} />
        <h2 id="score" className="mt-4 type-section scroll-mt-4">
          Score
        </h2>
        {locked && (
          <p className="mt-2 rounded-field border-[1.5px] border-ink-soft px-3 py-2">
            <span className="font-semibold">Scoring is locked.</span> Your scores are read-only.
          </p>
        )}
        {!seeded ? (
          <div role="status" aria-busy="true" aria-label="Loading your scores">
            <Skeleton className="mt-3 h-20 w-full" />
            <Skeleton className="mt-3 h-20 w-full" />
          </div>
        ) : (
          <>
            <div>
              {rubric.map((c, i) => (
                <div key={c.id} className="border-b border-rule py-3">
                  <CritHead c={c} all={rubric} />
                  {locked ? (
                    <p className="mt-1">
                      Your score: {scores[c.id] == null ? "—" : Number(scores[c.id]) || 0} / {Number(c.max_points) || 0}
                    </p>
                  ) : (
                    <Chips c={c} value={scores[c.id]} onPick={(p) => pick(c, p)} first={i === 0} />
                  )}
                </div>
              ))}
            </div>
            {locked ? (
              <div className="mt-4">
                <p className="type-label">Comment for the team</p>
                <p className={cn("mt-1", !comment && "text-ink-soft")}>{comment || "No comment."}</p>
                <p className="mt-1 text-sm text-ink-soft">Not shown with your name.</p>
              </div>
            ) : (
              <div className="mt-4">
                <Label htmlFor="jsc-comment">Comment for the team</Label>
                <Textarea
                  id="jsc-comment"
                  ref={commentBox}
                  data-tour="comment"
                  maxLength={1000}
                  rows={3}
                  placeholder="Share useful feedback"
                  value={comment}
                  aria-describedby="jsc-comment-help"
                  onChange={(e) => {
                    setComment(e.target.value);
                    clearTimeout(timer.current);
                    if (hasScore()) timer.current = window.setTimeout(() => void flushComment(), 900);
                  }}
                />
                <p id="jsc-comment-help" className="mt-1 text-sm text-ink-soft">
                  {Object.keys(scores).length ? "Optional. Not shown with your name. Saves automatically." : "Optional. Not shown with your name. Saves once you tap a score."}
                </p>
                <p className="text-sm text-ink-soft" aria-live="polite">
                  {line}
                </p>
              </div>
            )}
            {!locked && (
              <div className="mt-5 rounded-field border-[1.5px] border-bad px-4 py-3">
                <h2 className="type-label">Know this team?</h2>
                <Button variant="danger" className="mt-2" data-tour="conflict" disabled={conflictBusy} onClick={conflict}>
                  I know someone on this team
                </Button>
              </div>
            )}
          </>
        )}
        <div role="status">{guard.text && <p className="mt-4 rounded-field border-[1.5px] border-ink-soft bg-card px-3 py-2">{guard.text}</p>}</div>
        <div className="sticky bottom-[var(--sh-bottom-bar,0px)] z-20 -mx-4 mt-4 md:-mx-8 md:px-8 flex flex-wrap items-center gap-2 border-t border-rule bg-paper px-4 py-2 lg:static lg:mx-0 lg:border-t-0 lg:px-0">
          <Button data-tour="prev" disabled={index <= 0} onClick={prev}>
            Previous
          </Button>
          {!locked && (
            <Button variant="primary" data-tour="save-next" disabled={!seeded} onClick={next}>
              {last ? "Save & finish" : "Save & next"}
            </Button>
          )}
          <span className="ml-auto hidden text-sm text-ink-soft md:inline">
            {!locked && (
              <>
                <kbd className="rounded-sm border border-rule px-1 font-mono">N</kbd> next ·{" "}
              </>
            )}
            <kbd className="rounded-sm border border-rule px-1 font-mono">P</kbd> previous
          </span>
        </div>
      </div>
      {tour}
    </div>
  );
}

export default function Scoring({ match }: ScreenProps) {
  const slug = match.slug!;
  const d = useEventDetail(slug).data!;
  const { search } = useLocation();
  const navigate = useNavigate();
  const queue = useQueue(slug);
  const rubric = useRubric(slug);
  const asked = new URLSearchParams(search).get("team") || "";
  const rows = useMemo(() => queue.data?.queue || [], [queue.data]);
  const team = pickTeam(asked, rows);
  const { locked: lockedAt, resultsOut, notOpen } = eventFlags(d.event);
  const locked = lockedAt || resultsOut || !d.event.scoring_open;

  // The site and the video frame only under the judge address's own policy:
  // arriving here from another screen inside the app loads the page again.
  const wrongPolicy = serverMode() && !onJudgeDocument((performance.getEntriesByType("navigation")[0] as PerformanceNavigationTiming | undefined)?.name);
  useEffect(() => {
    if (wrongPolicy) location.reload();
  }, [wrongPolicy]);
  // An address with no team (or one no longer on the list) names the one opened.
  useEffect(() => {
    if (team && team !== asked) {
      const p = new URLSearchParams(search);
      p.set("team", team);
      navigate({ search: "?" + p }, { replace: true });
    }
  }, [team, asked, search, navigate]);

  // This judge's walking order, kept for the session (the queue's own order reshuffles as anyone scores).
  const order = useMemo(() => {
    const key = "sh-judge-order-" + slug;
    let stored: unknown = [];
    try {
      stored = JSON.parse(sessionStorage.getItem(key) || "[]");
    } catch {
      stored = [];
    }
    const ids = walkOrder(stored, rows);
    try {
      sessionStorage.setItem(key, JSON.stringify(ids));
    } catch {
      /* storage blocked */
    }
    return ids.map((id) => rows.find((r) => r.team_id === id)!).filter(Boolean);
  }, [rows, slug]);

  const back = (
    <p className="text-center">
      <AppLink to={href("judge", slug, "queue")}>Back to My teams</AppLink>
    </p>
  );
  if (wrongPolicy) return null;
  if (queue.isPending || rubric.isPending)
    return (
      <div className="mt-6" role="status" aria-busy="true" aria-label="Loading the team">
        <Skeleton className="h-11 w-48" />
        <Skeleton className="mt-4 h-64 w-full" />
      </div>
    );
  if (queue.isError)
    return (
      <>
        <h1 className="sr-only">Score entry</h1>
        <Failed error={queue.error} retry={() => queue.refetch()} />
        {back}
      </>
    );
  if (!team)
    return (
      <>
        <h1 className="sr-only">Score entry</h1>
        <EmptyState title="Nothing to score">Nothing assigned to you yet. Check back once the organiser assigns judges.</EmptyState>
        {back}
      </>
    );
  const criteria = (rubric.isSuccess && rubric.data.criteria) || [];
  if (!criteria.length)
    return (
      <>
        <h1 className="sr-only">Score entry</h1>
        <EmptyState title="No rubric yet">The organiser has not set a rubric yet.</EmptyState>
        {back}
      </>
    );
  if (notOpen)
    return (
      <>
        <h1 className="sr-only">Score entry</h1>
        <NotOpen slug={slug} back="queue" />
      </>
    );
  const index = order.findIndex((r) => r.team_id === team);
  if (index < 0) return null;
  return <Score key={team} d={d} queue={queue.data} order={order} row={order[index]} index={index} rubric={criteria} locked={locked} />;
}
