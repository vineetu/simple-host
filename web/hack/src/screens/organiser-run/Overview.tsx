// Home (/e/{slug}/manage?s=overview): the organiser's event home. It always
// says the one next thing to do and does it after one confirm (the server
// chooses the step: next_step / checklist.next), with this stage's
// checklist, what needs the organiser, a snapshot, the join and judge links,
// Change stage, End event, and the walkthrough. Every move then offers to
// tell participants (Moves.tsx).
import { useLocation } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { Button } from "@kit/components/ui/button";
import { CopyButton } from "@kit/components/ui/copy";
import { Input } from "@kit/components/ui/input";
import { Notice } from "@kit/components/ui/notice";
import { PageTitle, Section } from "@kit/components/ui/page-shell";
import { Stat, Stats } from "@kit/components/ui/stat";
import { useToast } from "@kit/components/ui/toast";
import { cn } from "@kit/lib/utils";
import { useHack } from "@/lib/client";
import { safeHref } from "@/lib/labels";
import { href, needHref } from "@/routes/resolve";
import { useEventDetail } from "@/app/data";
import { AppLink } from "@/app/nav";
import { Loading } from "@/app/states";
import type { ScreenProps } from "@/routes/registry";
import { RunFrame, errText, useRefreshEvent } from "./common";
import {
  CHECKLIST_GROUPS,
  cardContent,
  checklistActionLabel,
  chooseStep,
  headsUp,
  itemDetail,
  judgingPoolLine,
  noTrackLine,
  publishedRows,
  scoresChangedSincePublish,
  type ChecklistItem,
  type Content,
  type OrgView,
  type Standing,
} from "./data-overview";
import { SetupSummary, useMoves } from "./Moves";
import { OrganiserWalkthrough } from "./Walkthrough";

/** canScoreAsJudge: an organiser who also judges, once judging has started. */
const canScoreAsJudge = (d: OrgView) => !!d.can?.judge && ["judging", "results", "archived"].includes(d.event.stage);

/** The reads Home makes beside GET E, only those the step needs (organiser-overview.js). */
function useExtras(d: OrgView, stage: string | undefined) {
  const api = useHack();
  const slug = d.event.slug;
  const ev = d.event;
  const checkRepublish = !d.admin_view && !ev.taken_down && ["results", "archived"].includes(ev.stage) && !!ev.results_published;
  return useQuery({
    queryKey: ["overview-extras", slug, stage, checkRepublish],
    queryFn: async () => {
      const soft = <T,>(p: Promise<T>) => p.catch(() => null);
      const [settings, apps, teams, dash, pub, prev] = await Promise.all([
        soft(api.judging.settings(slug)),
        stage === "building" ? soft(api.people.applications(slug)) : null,
        stage === "judging" ? soft(api.teams.list(slug)) : null,
        stage === "judging" ? soft(api.judging.dashboard(slug)) : null,
        checkRepublish ? soft(api.voting.results(slug)) : null,
        checkRepublish ? soft(api.judging.preview(slug)) : null,
      ]);
      const s = settings as { judges?: unknown[]; assignment_mode?: string } | null;
      const list = (teams as { teams?: { name?: string; track?: unknown }[] } | null)?.teams;
      return {
        judges: s?.judges ? s.judges.length : undefined,
        pending: Array.isArray(apps) ? (apps as { status?: string }[]).filter((a) => a.status === "pending").length : 0,
        noTrack: s?.assignment_mode === "panel" && Array.isArray(list) ? list.filter((t) => !t.track).map((t) => t.name || "A team") : [],
        pool: (dash as { entries?: { judged?: number; no_entry?: number; excluded?: number } } | null)?.entries || null,
        republish: !!prev && scoresChangedSincePublish(publishedRows(pub), ((prev as { results?: Standing[] }).results || []) as Standing[]),
      };
    },
  });
}

function Sentence({ c }: { c: Content }) {
  if (c.link)
    return (
      <p className="mt-1 text-ink-soft">
        {c.link[0]}
        <AppLink to={c.link[2]}>{c.link[1]}</AppLink>
        {c.link[3]}
      </p>
    );
  return c.sentence ? <p className="mt-1 text-ink-soft">{c.sentence}</p> : null;
}

function ChecklistRow({ item, n, next, slug, locked }: { item: ChecklistItem; n: number; next?: string; slug: string; locked: boolean }) {
  const api = useHack();
  const toast = useToast();
  const refresh = useRefreshEvent(slug);
  const done = item.state === "done";
  const opted = item.state === "not_needed";
  const now = !done && next === item.id;
  const detail = itemDetail(item);
  const mark = async (restore: boolean) => {
    try {
      await (restore ? api.event.checklistNeeded(slug, item.id) : api.event.checklistNotNeeded(slug, item.id));
      await refresh();
      toast(restore ? item.label + " is back on the list" : item.label + ": not needed", { undo: { label: "Undo", run: () => void mark(!restore) } });
    } catch (e) {
      toast(errText(e), { bad: true });
    }
  };
  return (
    <li className={cn("flex flex-wrap items-center gap-x-3 gap-y-1 border-b border-rule py-2 last:border-b-0", (item.optional || item.settled) && !now && "text-ink-soft")} data-checklist-id={item.id}>
      <span
        aria-hidden="true"
        className={cn(
          "grid size-7 flex-none place-items-center rounded-tag border-[1.5px] text-sm",
          done ? "border-good bg-good text-on-accent" : now ? "border-accent text-accent" : "border-rule text-ink-soft",
        )}
      >
        {done ? "✓" : n}
      </span>
      <span className="min-w-0 flex-[1_1_12rem]">
        <span className={cn("block", now && "font-semibold text-ink")}>
          {item.label}
          <span className="sr-only">{done ? " (done)" : now ? " (next)" : ""}</span>
        </span>
        {detail && <span className="block text-sm text-ink-soft">{detail}</span>}
      </span>
      {!done && (
        <span className="flex flex-none flex-wrap gap-2">
          {!opted && item.actionable && item.link?.href && (
            <Button asChild>
              <AppLink to={item.link.href}>{checklistActionLabel(item)}</AppLink>
            </Button>
          )}
          {item.can_dismiss && (
            <Button variant="ghost" disabled={locked} onClick={() => mark(opted)}>
              {opted ? "Use this item" : "Not needed"}
            </Button>
          )}
        </span>
      )}
    </li>
  );
}

function StageChecklist({ d, locked }: { d: OrgView; locked: boolean }) {
  const cl = d.checklist;
  if (!cl) return null;
  const items = cl.items || [];
  let n = 0;
  const list = (rows: ChecklistItem[]) => (
    <ol className="m-0 list-none p-0">
      {rows.map((it) => (
        <ChecklistRow key={it.id} item={it} n={++n} next={cl.next?.item_id} slug={d.event.slug} locked={locked} />
      ))}
    </ol>
  );
  return (
    <Section title="This stage" className="rounded-wobble border border-rule bg-card px-4 py-3 md:px-5">
      {CHECKLIST_GROUPS.map(([group, label]) => {
        const rows = items.filter((i) => (i.group || "current") === group);
        if (!rows.length) return null;
        if (!label) return <div key={group}>{list(rows)}</div>;
        return (
          <details key={group} open={group === "earlier_required" && rows.some((i) => i.actionable && i.state !== "done")} className="mt-2">
            <summary className="flex min-h-11 cursor-pointer items-center text-ink-soft">{label}</summary>
            {list(rows)}
          </details>
        );
      })}
    </Section>
  );
}

function LinkRow({ id, label, value }: { id: string; label: string; value: string }) {
  return (
    <div className="mt-3">
      <label htmlFor={id} className="mb-1.5 block text-sm text-ink-soft">
        {label}
      </label>
      <div className="flex gap-2">
        <Input id={id} readOnly value={value} className="font-mono text-sm" onFocus={(e) => e.currentTarget.select()} />
        <CopyButton text={value} />
      </div>
    </div>
  );
}

const NEED_ACTION: Record<string, string> = { application_new: "Review applications", team_inbound: "Read and reply" };

export default function Overview({ match }: ScreenProps) {
  const slug = match.slug!;
  const d = useEventDetail(slug).data as OrgView;
  const { search } = useLocation();
  const force = new URLSearchParams(search).get("walkthrough") === "1";
  const ev = d.event;
  const admin = !!d.admin_view;
  const archived = ev.stage === "archived";
  const counts = d.organiser?.counts || {};
  const settingsHref = href("organiser", slug, "settings");
  const firstStep = chooseStep(d, false);
  const atStage = firstStep.kind === "stage" ? firstStep.stage : undefined;
  const extras = useExtras(d, atStage);
  const moves = useMoves(d);
  if (extras.isPending)
    return (
      <RunFrame slug={slug} screen={match.id}>
        <PageTitle>Home</PageTitle>
        <Loading />
      </RunFrame>
    );
  const x = extras.data!;
  const step = chooseStep(d, x.republish);
  const c = cardContent(d, step, settingsHref);
  const judges = x.judges ?? counts.judges ?? 0;
  const atStartJudging = step.kind === "stage" && step.stage === "judging";
  const warning = headsUp(step, { pending: x.pending, onNoTeam: counts.on_no_team ?? 0, judges, noTrack: atStartJudging ? x.noTrack.length : 0 });
  const locked = admin;
  const host = safeHref(ev.url as string);

  const next = () => {
    if (step.kind === "publish_results") return moves.publish(c.confirmBody);
    if (c.isEnd) return moves.end();
    moves.confirm({
      title: c.button + "?",
      confirm: c.button!,
      stage: step.stage!,
      body: (
        <div className="space-y-3">
          <p>{c.confirmBody || c.sentence}</p>
          {atStartJudging && x.pool && <p>{judgingPoolLine(x.pool)}</p>}
          {warning && <p className="rounded-field border-[1.5px] border-bad px-3 py-2 text-sm text-bad">{warning}</p>}
          {atStartJudging && <SetupSummary slug={slug} />}
        </div>
      ),
    });
  };

  return (
    <RunFrame slug={slug} screen={match.id}>
      <div className="mt-8 flex flex-wrap items-center gap-x-3 gap-y-2">
        <h1 className="mr-auto type-page">Home</h1>
        <Button data-tour="change-stage" disabled={archived || locked} onClick={moves.picker}>
          Change stage
        </Button>
        {canScoreAsJudge(d) && (
          <Button asChild>
            <a href={href("judge", slug, "home")}>Score as a judge</a>
          </Button>
        )}
      </div>
      {host && (
        <p className="mt-1">
          <a href={host} target="_blank" rel="noopener noreferrer" className="inline-flex min-h-11 items-center [overflow-wrap:anywhere]">
            {host.replace(/^https?:\/\//, "")}
            <span className="sr-only"> (opens in a new tab)</span>
          </a>
        </p>
      )}
      {admin && <Notice tone="bad" title="Viewing as platform admin. Everything here is read only." />}

      <section
        aria-labelledby="next-step"
        className={cn("mt-4 rounded-wobble border-[1.5px] bg-card px-4 py-4 shadow-paper md:flex md:items-center md:gap-6 md:px-6", c.warn ? "border-bad" : "border-accent")}
      >
        <div className="min-w-0 flex-1">
          <p className="type-label text-ink-soft">Your next step</p>
          <h2 id="next-step" className="mt-0.5 type-section">
            {c.h2}
          </h2>
          <Sentence c={c} />
          {c.cueNote && <p className="mt-1 text-sm text-ink-soft">{c.cueNote}</p>}
        </div>
        {c.button &&
          (c.navHref ? (
            <Button variant="primary" className="mt-3 w-full md:mt-0 md:w-auto" data-tour="next-move" asChild>
              <AppLink to={c.navHref}>{c.button}</AppLink>
            </Button>
          ) : (
            <Button variant="primary" className="mt-3 w-full md:mt-0 md:w-auto" data-tour="next-move" disabled={c.blocked || locked} onClick={next}>
              {c.button}
            </Button>
          ))}
      </section>

      {atStartJudging && x.noTrack.length > 0 && (
        <Notice
          tone="bad"
          title={noTrackLine(x.noTrack.length).replace(/\.$/, ":") + " " + x.noTrack.join(", ") + "."}
          action={
            <Button asChild>
              <AppLink to={href("organiser", slug, "teams")}>Go to Teams</AppLink>
            </Button>
          }
        />
      )}

      <StageChecklist d={d} locked={locked} />

      {(d.needs_you || []).map((n, i) => (
        <Notice
          key={n.id || i}
          title={n.text}
          action={
            <Button asChild>
              <AppLink to={needHref("organiser", slug, n)}>{NEED_ACTION[n.kind] || "Open"}</AppLink>
            </Button>
          }
        />
      ))}

      <Section title="Snapshot">
        <Stats>
          <Stat value={counts.participants ?? 0} label="Participants" />
          <Stat value={counts.teams ?? 0} label="Teams" />
          <Stat value={judges} label="Judges" />
          <Stat value={counts.on_no_team ?? 0} label="Not on a team" />
        </Stats>
      </Section>

      {!archived && (
        <Section title="Share your event">
          <div data-tour="share" className="rounded-wobble border border-rule bg-card px-4 pt-1 pb-4 md:px-5">
            <LinkRow id="ov-join-url" label="Join link" value={d.organiser?.join_url || ""} />
            <LinkRow id="ov-judge-url" label="Judge link" value={d.organiser?.judge_url || ""} />
          </div>
        </Section>
      )}

      {!archived && !c.isEnd && (
        <Section title="Danger zone" className="rounded-field border-[1.5px] border-bad px-4 py-3">
          <p className="font-semibold">End the event</p>
          <p className="text-sm text-ink-soft">Ending locks sign-up and scoring for good. Team sites are removed after 30 days.</p>
          {(counts.participants ?? 0) > 0 ? (
            <Button variant="danger" className="mt-3" disabled={locked} onClick={moves.end}>
              End event
            </Button>
          ) : (
            <p className="mt-2 text-sm">
              Nobody joined, so there is nothing to end. You can delete the event in <AppLink to={settingsHref}>Settings</AppLink>.
            </p>
          )}
        </Section>
      )}
      {moves.dialog}
      <OrganiserWalkthrough slug={slug} force={force} ready />
    </RunFrame>
  );
}
