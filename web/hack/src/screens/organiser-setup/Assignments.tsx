// The assignment grid (/e/{slug}/manage?s=assignments[&grid=manual|panel]):
// in Manual mode which judges score each team, in Panel mode which judges
// sit on each track's panel. A table of ticks from 700 px, one card per
// team or track on a phone. Judges who know someone on a team are dimmed
// for it in Manual (Panel does not dim: conflicts are per team). The grid
// of the mode that is not saved, and every grid once judging starts, is
// read only.
import { useEffect, useState, type ReactNode } from "react";
import { useLocation, useNavigate } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Chips } from "@kit/components/ui/badge";
import { Button } from "@kit/components/ui/button";
import { Check } from "@kit/components/ui/choice";
import { EmptyState } from "@kit/components/ui/empty-state";
import { FieldMessage } from "@kit/components/ui/input";
import { Notice } from "@kit/components/ui/notice";
import { PageTitle } from "@kit/components/ui/page-shell";
import { Skeleton } from "@kit/components/ui/skeleton";
import { useToast } from "@kit/components/ui/toast";
import { cn } from "@kit/lib/utils";
import { useHack } from "@/lib/client";
import type { Conflict, JudgingSettings } from "@/lib/client/judging";
import { href } from "@/routes/resolve";
import type { ScreenProps } from "@/routes/registry";
import { useEventDetail } from "@/app/data";
import { AppLink } from "@/app/nav";
import { Failed } from "@/app/states";
import { errorText, lockReason, LockNote, useUnsavedGuard } from "./common";
import { conflicted, gridOf, key, payload, sameTicks, ticked, type Grid, type TeamRow } from "./judging-data";

type Row = { judge_id: string; team_id?: string; track_id?: string };
type Subject = { id: string; name: string };
type Judge = { id: string; name: string };

function useGridData(slug: string) {
  const api = useHack();
  return [
    useQuery({ queryKey: ["judging-settings", slug], queryFn: () => api.judging.settings(slug) as Promise<JudgingSettings> }),
    useQuery({ queryKey: ["org-teams", slug], queryFn: async () => ((await api.teams.list(slug)).teams as TeamRow[]) || [] }),
    useQuery({ queryKey: ["conflicts", slug], queryFn: async () => ((await api.judging.conflicts(slug)).conflicts as Conflict[]) || [] }),
    useQuery({ queryKey: ["assignments", slug], queryFn: async () => ((await api.judging.assignments(slug)).assignments as Row[]) || [] }),
    useQuery({ queryKey: ["judging-panels", slug], queryFn: async () => ((await api.judging.panels(slug)).panels as Row[]) || [] }),
  ] as const;
}

function GridBody({
  grid,
  subjects,
  judges,
  conflicts,
  ticks,
  setTicks,
  readOnly,
}: {
  grid: Grid;
  subjects: Subject[];
  judges: Judge[];
  conflicts: Conflict[];
  ticks: Set<string>;
  setTicks: (t: Set<string>) => void;
  readOnly: boolean;
}) {
  const toggle = (k: string, v: boolean) => {
    const next = new Set(ticks);
    if (v) next.add(k);
    else next.delete(k);
    setTicks(next);
  };
  const dim = (j: Judge, s: Subject) => grid === "manual" && conflicted(conflicts, j.id, s.id);
  return (
    <>
      {/* Wide: one row per team or track, one column per judge. */}
      <div className="mt-4 hidden overflow-x-auto rounded-field border border-rule bg-card md:block">
        <table className="w-full border-collapse text-left">
          <caption className="sr-only">{grid === "manual" ? "Which judges score each team" : "Which judges sit on each track’s panel"}</caption>
          <thead>
            <tr className="bg-paper-lo">
              <th scope="col" className="px-3 py-2 type-label text-ink-soft">
                {grid === "manual" ? "Team" : "Track"}
              </th>
              {judges.map((j) => (
                <th key={j.id} scope="col" className="px-3 py-2 type-label text-ink-soft">
                  {j.name}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {subjects.map((s) => (
              <tr key={s.id} className="border-t border-rule">
                <th scope="row" className="px-3 py-1 font-medium text-ink">
                  {s.name}
                </th>
                {judges.map((j) => {
                  const k = key(j.id, s.id);
                  const c = dim(j, s);
                  return (
                    <td key={j.id} className={cn("px-3 py-0.5", c && "bg-paper-lo")}>
                      <Check checked={!c && ticks.has(k)} disabled={c || readOnly} onChange={(v) => toggle(k, v)}>
                        {c ? <span className="text-sm text-ink-soft">Knows someone</span> : <span className="sr-only">{`${j.name} for ${s.name}`}</span>}
                      </Check>
                    </td>
                  );
                })}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {/* Phone: one card per team or track, its judges listed under it. */}
      <ul className="m-0 mt-4 grid list-none gap-3 p-0 md:hidden" aria-label={grid === "manual" ? "Teams" : "Tracks"}>
        {subjects.map((s) => (
          <li key={s.id} className="rounded-field border border-rule bg-card px-4 py-2">
            <h2 className="pt-1 type-section">{s.name}</h2>
            {judges.map((j) => {
              const k = key(j.id, s.id);
              const c = dim(j, s);
              return (
                <Check key={j.id} checked={!c && ticks.has(k)} disabled={c || readOnly} onChange={(v) => toggle(k, v)}>
                  {j.name}
                  {c && <span className="text-ink-soft"> · knows someone on {s.name}</span>}
                </Check>
              );
            })}
          </li>
        ))}
      </ul>
    </>
  );
}

export default function Assignments({ match }: ScreenProps) {
  const slug = match.slug!;
  const d = useEventDetail(slug).data!;
  const ev = d.event;
  const api = useHack();
  const qc = useQueryClient();
  const toast = useToast();
  const navigate = useNavigate();
  const { pathname, search } = useLocation();
  const loads = useGridData(slug);
  const [settings, teams, conflicts, assignments, panels] = loads;
  const grid = gridOf(new URLSearchParams(search).get("grid"), settings.data?.assignment_mode);
  const saved = settings.data && conflicts.data && assignments.data && panels.data ? ticked(grid, grid === "manual" ? assignments.data : panels.data, conflicts.data) : null;
  const savedKey = saved ? grid + ":" + [...saved].sort().join(",") : "";
  const [ticks, setTicks] = useState<Set<string> | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  // A newly loaded (or saved) grid replaces the ticks on screen.
  useEffect(() => {
    setTicks(saved);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [savedKey]);
  const dirty = !!(ticks && saved && !sameTicks(ticks, saved));
  useUnsavedGuard(dirty);

  const back = (
    <nav aria-label="Breadcrumb" className="mt-5 text-sm text-ink-soft">
      <AppLink to={href("organiser", slug, "judging")} className="inline-flex min-h-11 items-center">
        Judging
      </AppLink>
      <span aria-hidden="true"> / </span>
      <span aria-current="page">Assignment grid</span>
    </nav>
  );
  const title = (sub: string, action?: ReactNode) => (
    <div className="flex flex-wrap items-end justify-between gap-x-3 gap-y-2 [&>div:first-child]:mt-0">
      <PageTitle sub={sub}>Assignment grid</PageTitle>
      {action}
    </div>
  );

  const failed = loads.find((l) => l.isError);
  if (failed)
    return (
      <>
        {back}
        {title("Choose which judges score each team.")}
        <Failed error={failed.error} retry={() => loads.forEach((l) => l.refetch())} />
      </>
    );
  if (loads.some((l) => l.isPending) || !ticks)
    return (
      <>
        {back}
        {title("Choose which judges score each team.")}
        <div className="mt-6" role="status" aria-busy="true" aria-label="Loading the assignment grid">
          <Skeleton className="h-11 w-56" />
          <Skeleton className="mt-4 h-48 w-full" />
        </div>
      </>
    );

  const mode = settings.data!.assignment_mode || "open";
  const judges: Judge[] = (settings.data!.judges || []).map((j) => ({ id: j.user_id, name: j.name || j.email || "Someone" }));
  const tracks = (ev.tracks as { id: string; name: string }[] | undefined) || [];
  const subjects: Subject[] = grid === "manual" ? teams.data!.map((t) => ({ id: t.id, name: t.name || t.slug || "A team" })) : tracks.map((t) => ({ id: t.id, name: t.name }));
  const lock = lockReason(d);
  const wrongMode = (grid === "manual" && mode !== "manual") || (grid === "panel" && mode !== "panel");
  const started = !!ev.judging_locked_at || ev.stage === "judging";
  const nothing = !subjects.length || !judges.length;
  const readOnly = !!lock || wrongMode || started;

  const save = async () => {
    setBusy(true);
    setError("");
    try {
      const body = payload(grid, ticks);
      const r = grid === "manual" ? await api.judging.setAssignments(slug, body) : await api.judging.setPanels(slug, body);
      qc.setQueryData(grid === "manual" ? ["assignments", slug] : ["judging-panels", slug], ((grid === "manual" ? r.assignments : r.panels) as Row[]) || []);
      toast("Judging access saved");
    } catch (err) {
      setError(errorText(err));
    } finally {
      setBusy(false);
    }
  };
  const pick = (g: Grid) => {
    const p = new URLSearchParams(search);
    p.set("grid", g);
    navigate(pathname + "?" + p.toString());
  };

  return (
    <>
      {back}
      {title(
        grid === "manual" ? "Choose which judges score each team." : "Choose the judges for each track.",
        <Button variant="primary" onClick={save} disabled={readOnly || nothing || busy || !dirty}>
          {busy ? "Saving…" : "Save judging access"}
        </Button>,
      )}
      <FieldMessage tone="bad">{error}</FieldMessage>
      <LockNote text={lock} />
      <Chips<Grid>
        label="Grid"
        value={grid}
        onChange={pick}
        options={[
          ["manual", "Manual: judge by team"],
          ["panel", "Panel: judge by track"],
        ]}
      />
      {!lock && started && <Notice title="Judging has started.">Move the event back to Entries closed to change who judges which teams.</Notice>}
      {nothing ? (
        <EmptyState title={grid === "manual" ? "Add teams and judges first" : "Add tracks and judges first"}>
          {grid === "manual" ? "Teams appear here once people form them. Judges join with the judge link." : "Tracks are set on the Event page. Judges join with the judge link."}
        </EmptyState>
      ) : (
        <>
          {wrongMode && (
            <Notice title={`This grid is used only in ${grid === "manual" ? "Manual" : "Panel"} mode.`}>
              Switch the mode in{" "}
              <AppLink to={href("organiser", slug, "settings") + "&tab=judging"} className="underline">
                Judging settings
              </AppLink>
              .
            </Notice>
          )}
          <p className="mt-4 max-w-prose text-sm text-ink-soft">
            {grid === "manual"
              ? "Judges who know a team are greyed out for that team."
              : "Conflicts are per team. In Panel mode a judge can still sit on a track’s panel even if they know someone on one team in it. That team just won’t be in their list."}
          </p>
          <GridBody grid={grid} subjects={subjects} judges={judges} conflicts={conflicts.data!} ticks={ticks} setTicks={setTicks} readOnly={readOnly} />
          {!readOnly && (
            <p className="mt-3 text-sm text-ink-soft" role="status">
              {dirty ? "Unsaved changes" : "No unsaved changes"}
            </p>
          )}
        </>
      )}
    </>
  );
}
