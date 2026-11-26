// Judging (/e/{slug}/manage?s=judging): the operational side of judging.
// Configuration lives on Settings → Judging (a link at the top); here are
// the assignment grid's door, scoring Progress per team and per judge (with
// former judges, whose scores can be deleted), and the judges who know a
// team (conflicts), which an organiser adds and removes.
import { useState, type FormEvent } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Badge } from "@kit/components/ui/badge";
import { Button } from "@kit/components/ui/button";
import { DataTable, type ColumnDef } from "@kit/components/ui/data-table";
import { FieldMessage, Label, NativeSelect } from "@kit/components/ui/input";
import { Meter } from "@kit/components/ui/meter";
import { Notice } from "@kit/components/ui/notice";
import { PageTitle, Section } from "@kit/components/ui/page-shell";
import { Skeleton } from "@kit/components/ui/skeleton";
import { useToast } from "@kit/components/ui/toast";
import { useHack } from "@/lib/client";
import type { Conflict, Dashboard, JudgingSettings } from "@/lib/client/judging";
import { possessive } from "@/lib/labels";
import { href } from "@/routes/resolve";
import type { ScreenProps } from "@/routes/registry";
import { useEventDetail } from "@/app/data";
import { AppLink } from "@/app/nav";
import { Failed } from "@/app/states";
import { errorText, lockReason, LockNote } from "./common";
import { formerMeta, noTrackNote, teamProgress, type TeamRow } from "./judging-data";
import { TypedConfirm } from "./settings-ui";

type Former = NonNullable<Dashboard["former_judges"]>[number];
type JudgeProgress = NonNullable<Dashboard["judges"]>[number];

function useJudgingData(slug: string) {
  const api = useHack();
  const settings = useQuery({ queryKey: ["judging-settings", slug], queryFn: () => api.judging.settings(slug) as Promise<JudgingSettings> });
  const conflicts = useQuery({ queryKey: ["conflicts", slug], queryFn: async () => ((await api.judging.conflicts(slug)).conflicts as Conflict[]) || [] });
  const teams = useQuery({ queryKey: ["org-teams", slug], queryFn: async () => ((await api.teams.list(slug)).teams as TeamRow[]) || [] });
  const assignments = useQuery({ queryKey: ["assignments", slug], queryFn: async () => ((await api.judging.assignments(slug)).assignments as unknown[]) || [] });
  const dashboard = useQuery({ queryKey: ["judging-dashboard", slug], queryFn: () => api.judging.dashboard(slug) as Promise<Dashboard> });
  return { settings, conflicts, teams, assignments, dashboard };
}

type Common = { slug: string; settings: JudgingSettings; teams: TeamRow[]; conflicts: Conflict[]; frozen: boolean; lock: string; ended: boolean };

function Progress({ slug, settings, teams, conflicts, frozen, lock, ended, assignmentsCount, dashboard }: Common & { assignmentsCount: number; dashboard: ReturnType<typeof useJudgingData>["dashboard"] }) {
  const api = useHack();
  const qc = useQueryClient();
  const toast = useToast();
  const [deleting, setDeleting] = useState<Former | null>(null);
  const mode = settings.assignment_mode || "open";
  const untracked = mode === "panel" ? noTrackNote(teams) : "";

  if (dashboard.isPending)
    return (
      <Section title="Progress">
        <div role="status" aria-busy="true" aria-label="Loading progress">
          <Skeleton className="h-24 w-full" />
        </div>
      </Section>
    );
  if (dashboard.isError)
    return (
      <Section title="Progress">
        <Failed error={dashboard.error} retry={() => dashboard.refetch()} />
      </Section>
    );

  const d = dashboard.data;
  const teamRows = teamProgress(d.teams || [], settings);
  const judges = d.judges || [];
  const former = d.former_judges || [];
  const teamCols: ColumnDef<(typeof teamRows)[number]>[] = [
    { id: "team", header: "Team", accessorFn: (t) => t.name, meta: { phone: "title", wrap: true } },
    { id: "scored", header: "Scored", enableSorting: false, cell: ({ row }) => row.original.text, meta: { phone: "foot" } },
    {
      id: "short",
      header: () => <span className="sr-only">Coverage</span>,
      enableSorting: false,
      cell: ({ row }) => (row.original.short ? <Badge tone="down">{row.original.short}</Badge> : null),
      meta: { phone: "foot" },
    },
  ];
  const judgeCols: ColumnDef<JudgeProgress>[] = [
    { id: "judge", header: "Judge", accessorFn: (j) => j.judge_name || "A judge", meta: { phone: "title", wrap: true } },
    {
      id: "done",
      header: "Done",
      enableSorting: false,
      cell: ({ row: { original: j } }) => (
        <div className="min-w-40">
          <Meter label={(j.judge_name || "A judge") + " progress"} value={j.done_count} max={j.assigned_count} text={`${j.done_count} of ${j.assigned_count} done`} warnAt={2} />
        </div>
      ),
      meta: { phone: "foot" },
    },
  ];

  return (
    <Section title="Progress">
      <div className="flex flex-wrap items-center gap-3">
        <p className="mr-auto text-sm text-ink-soft">Scores in so far, per team and per judge.</p>
        {!ended && (
          <Button onClick={() => dashboard.refetch()} disabled={dashboard.isFetching}>
            {dashboard.isFetching ? "Refreshing…" : "Refresh"}
          </Button>
        )}
      </div>
      {mode === "automatic" && assignmentsCount === 0 && <Notice title="Assignments not generated yet">Automatic mode assigns judges when you start judging.</Notice>}
      {untracked && (
        <Notice
          tone="bad"
          title={untracked}
          action={
            <Button asChild>
              <AppLink to={href("organiser", slug, "teams")}>Go to Teams</AppLink>
            </Button>
          }
        />
      )}
      <h3 className="mt-4 mb-1 type-label text-ink-soft">Teams</h3>
      <DataTable label="Teams’ scoring progress" data={teamRows} columns={teamCols} getRowId={(t) => t.id} paged={false} sortable={false} empty={<p className="py-3 text-ink-soft">No teams yet.</p>} />
      {(judges.length > 0 || !former.length) && (
        <>
          <h3 className="mt-5 mb-1 type-label text-ink-soft">Judges</h3>
          <DataTable
            label="Judges’ scoring progress"
            data={judges}
            columns={judgeCols}
            getRowId={(j) => j.judge_id || j.judge_name || ""}
            paged={false}
            sortable={false}
            empty={<p className="py-3 text-ink-soft">No judges yet.</p>}
          />
        </>
      )}
      {former.length > 0 && (
        <>
          <h3 id="jg-former" className="mt-5 mb-1 type-label text-ink-soft">
            Former judges
          </h3>
          <ul aria-labelledby="jg-former" className="m-0 list-none rounded-field border border-rule bg-card px-3 py-1">
            {former.map((f) => (
              <li key={f.user_id} className="flex min-h-14 flex-wrap items-center gap-x-3 gap-y-1 border-b border-rule py-2 last:border-b-0">
                <span className="min-w-0 flex-[1_1_12rem]">
                  <span className="mr-2 text-ink">{f.display_name || "A judge"}</span>
                  <Badge>Left the event</Badge>
                  <span className="block text-sm text-ink-soft">{formerMeta(f)}</span>
                </span>
                <Button onClick={() => setDeleting(f)} disabled={frozen || !!lock}>
                  Delete scores
                </Button>
              </li>
            ))}
          </ul>
          {frozen && !lock && <p className="mt-1.5 text-sm text-ink-soft">Move the event back to Judging to change scores.</p>}
        </>
      )}
      {conflicts.length > 0 && (
        <p className="mt-4 max-w-prose text-sm text-ink-soft">
          A judge’s conflict excludes their earlier score for that team from coverage and from the team’s total and rank, even if it was scored before the conflict was declared.
        </p>
      )}
      {deleting && (
        <TypedConfirm
          open
          onOpenChange={(o) => !o && setDeleting(null)}
          title={`Delete ${possessive(deleting.display_name || "This judge")} scores?`}
          body={`${deleting.display_name || "This judge"} left the event. Their scores and comments for ${deleting.counted_teams} team${deleting.counted_teams === 1 ? "" : "s"} will be deleted and stop counting toward totals and ranks. Published results only change when you publish again.`}
          match={slug}
          confirmLabel="Delete scores"
          onConfirm={async () => {
            await api.judging.deleteFormerJudgeScores(slug, deleting.user_id);
            toast("Scores deleted");
            await qc.invalidateQueries({ queryKey: ["judging-dashboard", slug] });
          }}
        />
      )}
    </Section>
  );
}

function Conflicts({ slug, settings, teams, conflicts, frozen, lock, ended }: Common) {
  const api = useHack();
  const qc = useQueryClient();
  const toast = useToast();
  const judges = settings.judges || [];
  const [judge, setJudge] = useState("");
  const [team, setTeam] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const blocked = frozen || !!lock;
  const reload = () => Promise.all([qc.invalidateQueries({ queryKey: ["conflicts", slug] }), qc.invalidateQueries({ queryKey: ["judging-dashboard", slug] })]);
  const add = (team_id: string, judge_user_id: string) => api.judging.declareConflict(slug, { team_id, judge_user_id });
  const remove = (team_id: string, judge_id: string) => api.judging.removeJudgeConflict(slug, team_id, judge_id);
  const pickedJudge = judge || judges[0]?.user_id || "";
  const pickedTeam = team || teams[0]?.id || "";
  const failedUndo = (err: unknown) => toast(errorText(err), { bad: true });

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setError("");
    if (!pickedJudge || !pickedTeam) return setError("Choose a judge and a team.");
    setBusy(true);
    try {
      await add(pickedTeam, pickedJudge);
      await reload();
      toast("Conflict added", { undo: { label: "Undo", run: () => void remove(pickedTeam, pickedJudge).then(reload).catch(failedUndo) } });
    } catch (err) {
      setError(errorText(err));
    } finally {
      setBusy(false);
    }
  };
  const removeRow = async (c: Conflict) => {
    try {
      await remove(c.team_id, c.judge_id);
      await reload();
      toast(`${c.judge_name || "This judge"} can be assigned to ${c.team_name || "this team"} again.`, {
        undo: { label: "Undo", run: () => void add(c.team_id, c.judge_id).then(reload).catch(failedUndo) },
      });
    } catch (err) {
      toast(errorText(err), { bad: true });
    }
  };

  return (
    <Section title="Judges who know a team" note="A judge who knows someone on a team is not given that team to score.">
      {conflicts.length ? (
        <ul aria-label="Judges who know a team" className="m-0 list-none rounded-field border border-rule bg-card px-3 py-1">
          {conflicts.map((c) => (
            <li key={c.judge_id + c.team_id} className="flex min-h-14 flex-wrap items-center gap-x-3 gap-y-1 border-b border-rule py-2 last:border-b-0">
              <span className="min-w-0 flex-[1_1_12rem]">
                <span className="block text-ink">{c.judge_name || "A judge"}</span>
                <span className="block text-sm text-ink-soft">Knows someone on {c.team_name || "a team"}</span>
              </span>
              <Button variant="danger" disabled={blocked} onClick={() => removeRow(c)} aria-label={`Remove ${possessive(c.judge_name || "the judge")} conflict with ${c.team_name || "the team"}`}>
                Remove
              </Button>
            </li>
          ))}
        </ul>
      ) : (
        <p className="text-ink-soft">None yet.</p>
      )}
      {!ended &&
        (judges.length && teams.length ? (
          <form onSubmit={submit} className="mt-4 max-w-prose" aria-label="Add a conflict">
            <div className="flex flex-wrap items-end gap-2.5">
              <div className="min-w-0 flex-[1_1_12rem]">
                <Label htmlFor="cf-judge">Judge</Label>
                <NativeSelect id="cf-judge" className="w-full" value={pickedJudge} disabled={blocked} onChange={(e) => setJudge(e.target.value)}>
                  {judges.map((j) => (
                    <option key={j.user_id} value={j.user_id}>
                      {j.name || j.email || "Someone"}
                    </option>
                  ))}
                </NativeSelect>
              </div>
              <div className="min-w-0 flex-[1_1_12rem]">
                <Label htmlFor="cf-team">Team</Label>
                <NativeSelect id="cf-team" className="w-full" value={pickedTeam} disabled={blocked} onChange={(e) => setTeam(e.target.value)}>
                  {teams.map((t) => (
                    <option key={t.id} value={t.id}>
                      {t.name || t.slug}
                    </option>
                  ))}
                </NativeSelect>
              </div>
              <Button type="submit" disabled={blocked || busy}>
                Add conflict
              </Button>
            </div>
            <FieldMessage tone="bad">{error}</FieldMessage>
          </form>
        ) : (
          <p className="mt-3 text-sm text-ink-soft">Conflicts can be added once the event has judges and teams.</p>
        ))}
    </Section>
  );
}

export default function Judging({ match }: ScreenProps) {
  const slug = match.slug!;
  const d = useEventDetail(slug).data!;
  const ev = d.event;
  const q = useJudgingData(slug);
  const lock = lockReason(d);
  const frozen = !!ev.judging_locked_at;
  const ended = ev.stage === "archived";
  const canJudge = !!d.can?.judge && ["judging", "results", "archived"].includes(ev.stage);
  const head = (
    <div className="flex flex-wrap items-end justify-between gap-x-3">
      <PageTitle sub="Rubric, assignments and scoring progress.">Judging</PageTitle>
      {canJudge && (
        <Button asChild>
          <a href={href("judge", slug, "home")}>Score as a judge</a>
        </Button>
      )}
    </div>
  );

  const loads = [q.settings, q.conflicts, q.teams, q.assignments];
  if (loads.some((l) => l.isPending))
    return (
      <>
        {head}
        <div className="mt-7" role="status" aria-busy="true" aria-label="Loading judging">
          <Skeleton className="h-20 w-full" />
          <Skeleton className="mt-3 h-40 w-full" />
        </div>
      </>
    );
  const failed = loads.find((l) => l.isError);
  if (failed)
    return (
      <>
        {head}
        <Failed error={failed.error} retry={() => loads.forEach((l) => l.refetch())} />
      </>
    );
  const common: Common = { slug, settings: q.settings.data!, teams: q.teams.data!, conflicts: q.conflicts.data!, frozen, lock, ended };

  return (
    <>
      {head}
      <LockNote text={lock} />
      <p className="mt-4 flex flex-wrap items-center gap-x-2">
        <AppLink to={href("organiser", slug, "settings") + "&tab=judging"} className="inline-flex min-h-11 items-center">
          Judging settings
        </AppLink>
        <span className="text-sm text-ink-soft">Rubric, how judges are assigned, tie-break and when judging closes.</span>
      </p>
      <Section title="Assignments" note="Automatic assigns judges when you start judging. Manual and Panel use the assignment grid before judging.">
        <Button asChild>
          <AppLink to={href("organiser", slug, "assignments")}>Open the assignment grid</AppLink>
        </Button>
      </Section>
      <Progress {...common} assignmentsCount={q.assignments.data!.length} dashboard={q.dashboard} />
      <Conflicts {...common} />
      {frozen && !ev.results_published && (
        <p className="mt-6 text-ink-soft">
          Publish from <AppLink to={href("organiser", slug, "overview")}>Home</AppLink> when ready.
        </p>
      )}
    </>
  );
}
