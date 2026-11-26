// My teams (/e/{slug}/judge?s=queue[&tab=to-score|scored|conflicts]): the
// teams this judge is scoring, in three tabs: To score, Scored (with their
// own total), and Teams I know, where a judge says they know someone on a
// team (or takes it back), and can say once that they know nobody.
// Declaring and taking back a conflict each happen at once, with Undo.
import { useState } from "react";
import { useLocation } from "react-router";
import { useQueries } from "@tanstack/react-query";
import { Badge } from "@kit/components/ui/badge";
import { Button } from "@kit/components/ui/button";
import { Notice } from "@kit/components/ui/notice";
import { PageTitle } from "@kit/components/ui/page-shell";
import { Skeleton } from "@kit/components/ui/skeleton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@kit/components/ui/tabs";
import { useToast } from "@kit/components/ui/toast";
import type { ApiError } from "@kit/api/call";
import { paths, useHack, type EventDetail } from "@/lib/client";
import { MATURITY } from "@/lib/labels";
import { dateTime } from "@/lib/time";
import { href } from "@/routes/resolve";
import { useEventDetail } from "@/app/data";
import { AppLink } from "@/app/nav";
import { Failed } from "@/app/states";
import type { ScreenProps } from "@/routes/registry";
import { Screenshot } from "@/shared/Screenshot";
import { emptyToScore, eventFlags, hostLabel, knownList, openTab, pointsOf, rubricMax, siteHref, type QueueRow, type Scores } from "./data";
import { Acknowledge } from "./Checklist";
import { NewTab, NotOpen } from "./parts";
import { useMyConflicts, useQueue, useRefreshJudging, useRubric } from "./queries";

type Mode = "to-score" | "scored" | "readonly";

function subLine(row: QueueRow, ev: EventDetail["event"]): string {
  const at = row.submitted_at || row.updated_at;
  const submitted = row.incomplete ? "Not complete at the deadline" : at ? "Submitted " + dateTime(at, ev) : "";
  const team = row.title && row.title !== row.team.name ? row.team.name : "";
  return [team, submitted, String(row.tagline || "").trim()].filter(Boolean).join(" · ");
}

function Row({ row, mode, first, total, max, d }: { row: QueueRow; mode: Mode; first: boolean; total?: number; max: number; d: EventDetail }) {
  const slug = d.event.slug;
  const [shot, setShot] = useState(false);
  const title = row.title || row.team.name || "Entry";
  const sub = subLine(row, d.event);
  const site = siteHref(row);
  const video = row.video?.url && hostLabel(row.video.url) ? row.video.url : "";
  const open = site ? ["Open site", site] : video ? ["Watch video", video] : null;
  const mat = row.maturity ? MATURITY[row.maturity] : undefined;
  const score = total != null ? `${total}/${max}` : "";
  const to = href("judge", slug, "scoring") + "&team=" + encodeURIComponent(row.team_id);
  return (
    <li className="border-b border-rule py-3 last:border-b-0">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
        <div className="min-w-0 flex-[1_1_16rem]">
          <p className="font-semibold">{title}</p>
          {sub && <p className="text-sm text-ink-soft">{sub}</p>}
          {(row.track?.name || mat || row.incomplete) && (
            <p className="mt-1 flex flex-wrap gap-1.5">
              {row.track?.name && <Badge>{row.track.name} track</Badge>}
              {mat && <Badge tone={row.maturity === "working" ? "ok" : "neutral"}>{mat.label}</Badge>}
              {row.incomplete && <Badge tone="down">Incomplete</Badge>}
            </p>
          )}
        </div>
        <div className="flex flex-wrap items-center gap-2">
          {open && (
            <NewTab to={open[1]}>
              {open[0]} ({hostLabel(open[1])})
            </NewTab>
          )}
          {row.has_screenshot && (
            <Button aria-expanded={shot} onClick={() => setShot(!shot)}>
              {shot ? "Hide screenshot" : "View screenshot"}
            </Button>
          )}
          {mode === "readonly" ? (
            row.done ? (
              <Badge tone="ok">Your score: {score}</Badge>
            ) : (
              <Badge>Not scored</Badge>
            )
          ) : mode === "scored" ? (
            <>
              <Button asChild>
                <AppLink to={to}>Edit your score</AppLink>
              </Button>
              <Badge tone="ok">Scored {score}</Badge>
            </>
          ) : (
            <Button variant={first ? "primary" : "secondary"} asChild>
              <AppLink to={to}>Score this team</AppLink>
            </Button>
          )}
        </div>
      </div>
      {shot && <Screenshot path={paths.teamScreenshot(slug, row.team.slug)} alt={"Screenshot of " + (row.team.name || title)} />}
    </li>
  );
}

function Known({ d, list, locked }: { d: EventDetail; list: ReturnType<typeof knownList>; locked: boolean }) {
  const api = useHack();
  const toast = useToast();
  const refresh = useRefreshJudging(d.event.slug);
  const [busy, setBusy] = useState("");
  const slug = d.event.slug;
  const act = async (team: string, name: string, declare: boolean, hadScore: boolean, quiet = false) => {
    setBusy(team);
    try {
      if (declare) await api.judging.declareConflict(slug, { team_id: team });
      else await api.judging.removeConflict(slug, team);
      await refresh();
      if (!quiet)
        toast(declare ? `You won't score ${name}. Any scores you gave it won't count while the conflict is declared.` : hadScore ? `Your score for ${name} counts again.` : `You can score ${name} now.`, {
          undo: { label: "Undo", run: () => void act(team, name, !declare, hadScore, true) },
        });
    } catch (e) {
      if ((e as ApiError).status !== 401) toast((e as Error).message || "Cannot reach the server.", { bad: true });
    } finally {
      setBusy("");
    }
  };
  return (
    <>
      {list.length ? (
        <ul id="conflicts" className="m-0 list-none rounded-wobble border border-rule bg-card px-4 py-1">
          {list.map((c) => (
            <li key={c.team_id} className="flex min-h-14 flex-wrap items-center gap-x-4 gap-y-2 border-b border-rule py-2 last:border-b-0">
              <span className="min-w-0 flex-[1_1_12rem]">
                <span className="block font-semibold">{c.team_name}</span>
                {c.mine && <span className="block text-sm text-ink-soft">You know someone on this team</span>}
              </span>
              {!locked &&
                (c.mine ? (
                  <Button disabled={busy === c.team_id} onClick={() => act(c.team_id, c.team_name, false, !!c.mine?.had_score)}>
                    I don’t know this team after all
                  </Button>
                ) : (
                  <Button disabled={busy === c.team_id} onClick={() => act(c.team_id, c.team_name, true, false)}>
                    Know this team?
                  </Button>
                ))}
            </li>
          ))}
        </ul>
      ) : (
        <p className="text-ink-soft">No teams to review yet.</p>
      )}
      {!locked && (
        <Acknowledge d={d} id="j.conflicts" label="I don't know any of these teams" help="Checked the list and know nobody on it? Say so once." doneText="Saved: you don't know anyone on these teams." />
      )}
    </>
  );
}

export default function Queue({ match }: ScreenProps) {
  const slug = match.slug!;
  const d = useEventDetail(slug).data!;
  const { search } = useLocation();
  const queue = useQueue(slug);
  const conflicts = useMyConflicts(slug);
  const rubric = useRubric(slug);
  const api = useHack();
  const rows = queue.data?.queue || [];
  const scored = rows.filter((r) => r.done);
  const toScore = rows.filter((r) => !r.done);
  // Own totals, only for the teams this judge has scored (there is no server total for one judge).
  const totals = useQueries({
    queries: scored.map((r) => ({ queryKey: ["judge-scores", slug, r.team_id], queryFn: async () => (await api.judging.scores(slug, r.team_id)) as unknown as Scores })),
  });
  const { locked, resultsOut, notOpen } = eventFlags(d.event);
  const [tab, setTab] = useState<string | null>(null);
  const title = <PageTitle sub="Teams you are scoring.">My teams</PageTitle>;

  if (queue.isPending || conflicts.isPending)
    return (
      <>
        {title}
        <div className="mt-6" role="status" aria-busy="true" aria-label="Loading your teams">
          <Skeleton className="h-11 w-72" />
          <Skeleton className="mt-4 h-20 w-full" />
          <Skeleton className="mt-2 h-20 w-full" />
        </div>
      </>
    );
  if (queue.isError || conflicts.isError)
    return (
      <>
        {title}
        <Failed error={queue.error || conflicts.error} retry={() => (queue.refetch(), conflicts.refetch())} />
        <p className="text-center">
          <AppLink to={href("judge", slug, "home")}>Back to Overview</AppLink>
        </p>
      </>
    );
  if (notOpen)
    return (
      <>
        {title}
        <NotOpen slug={slug} back="home" />
      </>
    );

  const mine = conflicts.data;
  const max = rubricMax(rubric.data?.criteria);
  const total = (team: string) => {
    const k = scored.findIndex((r) => r.team_id === team);
    const s = k >= 0 ? totals[k]?.data : undefined;
    return s ? pointsOf(s) : undefined;
  };
  const readonly = resultsOut || locked;
  const counts = { "to-score": toScore.length, scored: scored.length, conflicts: mine.length };
  const active = tab || openTab(new URLSearchParams(search).get("tab"), counts);
  const list = (items: QueueRow[], mode: Mode) => (
    <ul className="m-0 list-none rounded-wobble border border-rule bg-card px-4">
      {items.map((r, i) => (
        <Row key={r.team_id} row={r} mode={readonly ? "readonly" : mode} first={i === 0} total={total(r.team_id)} max={max} d={d} />
      ))}
    </ul>
  );
  return (
    <>
      {title}
      {resultsOut ? <Notice title="Results are out." /> : locked ? <Notice title="Scoring is locked.">Scores can no longer be changed.</Notice> : null}
      <Tabs value={active} onValueChange={setTab} className="mt-5">
        <TabsList aria-label="My teams filters">
          <TabsTrigger value="to-score">
            To score <span className="text-ink-soft">{counts["to-score"]}</span>
          </TabsTrigger>
          <TabsTrigger value="scored">
            Scored <span className="text-ink-soft">{counts.scored}</span>
          </TabsTrigger>
          <TabsTrigger value="conflicts">
            Teams I know <span className="text-ink-soft">{counts.conflicts}</span>
          </TabsTrigger>
        </TabsList>
        <TabsContent value="to-score">{toScore.length ? list(toScore, "to-score") : <p className="text-ink-soft">{emptyToScore(queue.data, scored.length)}</p>}</TabsContent>
        <TabsContent value="scored">{scored.length ? list(scored, "scored") : <p className="text-ink-soft">No teams scored yet.</p>}</TabsContent>
        <TabsContent value="conflicts">
          <Known d={d} list={knownList(queue.data, mine)} locked={readonly} />
        </TabsContent>
      </Tabs>
    </>
  );
}
