// Entries (/e/{slug}/manage?s=entries): the counts, the teams with more time
// (give or clear an extension), every team's entry with its status and site,
// Exclude or Include by hand, and the entries download with what the team
// sites use. What teams hand in is set in Settings → Entries (linked here).
// ?team=<slug> (from an entry's Give more time) picks that team in Extensions.
import { useEffect, useRef, useState } from "react";
import { useLocation } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { Badge } from "@kit/components/ui/badge";
import { Button } from "@kit/components/ui/button";
import { DataTable, type ColumnDef } from "@kit/components/ui/data-table";
import { AskDialog } from "@kit/components/ui/dialog";
import { EmptyState } from "@kit/components/ui/empty-state";
import { Field, Input, NativeSelect } from "@kit/components/ui/input";
import { PageTitle, Section } from "@kit/components/ui/page-shell";
import { Skeleton } from "@kit/components/ui/skeleton";
import { Stat, Stats } from "@kit/components/ui/stat";
import { useToast } from "@kit/components/ui/toast";
import { paths, useHack, type EventDetail } from "@/lib/client";
import { dateTime } from "@/lib/time";
import { useEventDetail } from "@/app/data";
import { AppLink } from "@/app/nav";
import { Failed } from "@/app/states";
import type { ScreenProps } from "@/routes/registry";
import { errText, useDownload, useRefreshEvent } from "./common";
import {
  detailHref,
  entryRulesHref,
  fmtBytes,
  progress,
  remindersHref,
  rowAction,
  siteWords,
  status,
  usageTotal,
  type EntriesList,
  type EntryItem,
  type RunTeam,
  type TeamsList,
  type Usage,
} from "./data-entries";
import { LockNote, writeLock } from "@/shared/locks";

const Title = () => <PageTitle sub="Review entries and give teams more time.">Entries</PageTitle>;

export default function Entries({ match }: ScreenProps) {
  const slug = match.slug!;
  return (
    <>
      <Body slug={slug} />
    </>
  );
}

function Body({ slug }: { slug: string }) {
  const api = useHack();
  const d = useEventDetail(slug).data!;
  const entries = useQuery({ queryKey: ["entries", slug], queryFn: () => api.entries.list(slug) as Promise<EntriesList> });
  const teams = useQuery({ queryKey: ["teams", slug], queryFn: () => api.teams.list(slug) as Promise<TeamsList> });
  const usage = useQuery({ queryKey: ["usage", slug], queryFn: () => api.entries.usage(slug) as Promise<Usage> });

  if (entries.isPending || teams.isPending)
    return (
      <>
        <Title />
        <div className="mt-7" role="status" aria-busy="true" aria-label="Loading entries">
          <Skeleton className="h-16 w-full" />
          <Skeleton className="mt-6 h-32 w-full" />
          <Skeleton className="mt-6 h-48 w-full" />
        </div>
      </>
    );
  if (entries.isError)
    return (
      <>
        <Title />
        <Failed error={entries.error} retry={() => entries.refetch()} />
      </>
    );
  return <Loaded slug={slug} d={d} list={entries.data} teams={teams.data?.teams || []} usage={usage.data} />;
}

function Loaded({ slug, d, list, teams, usage }: { slug: string; d: EventDetail; list: EntriesList; teams: RunTeam[]; usage?: Usage }) {
  const api = useHack();
  const toast = useToast();
  const refresh = useRefreshEvent(slug);
  const download = useDownload();
  const ev = d.event;
  const lock = writeLock(d);
  const archived = ev.stage === "archived";
  const counts = list.counts || {};
  const rows = list.entries || [];
  const when = (iso: string) => dateTime(iso, ev);
  const reload = () => refresh(["entries", slug], ["teams", slug]);

  const setIncluded = async (e: EntryItem, include: boolean, undoable = true) => {
    try {
      await api.entries.exclude(slug, e.team.slug, !include);
    } catch (err) {
      toast(errText(err), { bad: true });
      return;
    }
    await reload();
    toast(include ? `${e.team.name} is back in judging.` : `${e.team.name} is excluded from judging. Judges stop seeing it and its scores stop counting.`, {
      undo: undoable ? { label: "Undo", run: () => void setIncluded(e, !include, false) } : undefined,
    });
  };

  const columns: ColumnDef<EntryItem, unknown>[] = [
    {
      id: "team",
      header: "Team",
      accessorFn: (e) => e.team.name,
      cell: ({ row }) => <AppLink to={detailHref(slug, row.original.team.slug)}>{row.original.team.name}</AppLink>,
      meta: { phone: "title" },
    },
    {
      id: "progress",
      header: "Progress",
      enableSorting: false,
      cell: ({ row }) => {
        const e = row.original;
        const t = teams.find((x) => x.slug === e.team.slug);
        const bits = [progress(e, when)];
        if (t?.extended && t.deadline_override) bits.push("More time until " + when(t.deadline_override));
        bits.push(siteWords(e));
        return <span className="text-ink-soft">{bits.join(" · ")}</span>;
      },
      meta: { phone: "foot", wrap: true },
    },
    {
      id: "status",
      header: "Status",
      accessorFn: (e) => status(e).label,
      cell: ({ row }) => {
        const s = status(row.original);
        return <Badge tone={s.tone}>{s.label}</Badge>;
      },
      meta: { phone: "end" },
    },
    {
      id: "action",
      header: "Judging",
      enableSorting: false,
      cell: ({ row }) => {
        const e = row.original;
        const act = rowAction(e);
        if (!act) return null;
        return act === "include" ? (
          <Button disabled={!!lock} onClick={() => setIncluded(e, true)} aria-label={"Include " + e.team.name}>
            Include
          </Button>
        ) : (
          <Button variant="danger" disabled={!!lock} onClick={() => setIncluded(e, false)} aria-label={"Exclude " + e.team.name}>
            Exclude
          </Button>
        );
      },
      meta: { phone: "foot", align: "right" },
    },
  ];

  return (
    <>
      <Title />
      <LockNote reason={lock} />
      <div className="mt-5">
        <Stats>
          <Stat value={counts.submitted || 0} label="submitted" />
          <Stat value={counts.drafts || 0} label="drafts" />
          <Stat value={counts.not_started || 0} label="not started" />
          {!!counts.not_submitted && <Stat value={counts.not_submitted} label="incomplete" />}
          <Stat value={counts.sites_live || 0} label="sites live" />
        </Stats>
      </div>
      <p className="mt-3 flex flex-wrap gap-x-4">
        <AppLink to={entryRulesHref(slug)} className="inline-flex min-h-11 items-center">
          Entry settings
        </AppLink>
        <AppLink to={remindersHref(slug)} className="inline-flex min-h-11 items-center">
          Reminders
        </AppLink>
      </p>
      <Extensions slug={slug} d={d} teams={teams} lock={lock} archived={archived} reload={reload} />
      <Section title="Entries" count={teams.length + (teams.length === 1 ? " team" : " teams")}>
        <DataTable
          label="Entries"
          data={rows}
          columns={columns}
          getRowId={(e) => e.team.slug}
          empty={<EmptyState title="No entries yet">They show up here once teams start filling theirs in.</EmptyState>}
        />
      </Section>
      <Section title="Export">
        <Button onClick={() => download(paths.csv(slug, "entries"), slug + "-entries.csv")}>Download entries (CSV)</Button>
        <p className="mt-2 text-sm text-ink-soft">This event’s team sites use {usage ? fmtBytes(usageTotal(usage)) : "…"}</p>
      </Section>
    </>
  );
}

function Extensions({ slug, d, teams, lock, archived, reload }: { slug: string; d: EventDetail; teams: RunTeam[]; lock: string; archived: boolean; reload: () => Promise<unknown> }) {
  const api = useHack();
  const toast = useToast();
  const ev = d.event;
  const { search } = useLocation();
  const want = new URLSearchParams(search).get("team") || "";
  const box = useRef<HTMLElement>(null);
  const [team, setTeam] = useState(() => (teams.some((t) => t.slug === want) ? want : teams[0]?.slug || ""));
  const [until, setUntil] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [clearing, setClearing] = useState<RunTeam | null>(null);
  const extended = teams.filter((t) => t.extended);
  const when = (iso?: string | null) => dateTime(iso, ev);

  useEffect(() => {
    if (want) box.current?.scrollIntoView?.({ behavior: "smooth", block: "start" });
  }, [want]);

  const give = async () => {
    if (!until) {
      setError("Choose a date and time.");
      return;
    }
    setError("");
    setBusy(true);
    try {
      await api.teams.deadline(slug, team, { deadline: until });
      await reload();
      setUntil("");
      toast("Saved");
    } catch (e) {
      setError(errText(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <section ref={box} id="ext" className="mt-7" aria-labelledby="ext-title">
      <h2 id="ext-title" className="mb-1 type-section">
        Extensions
      </h2>
      {extended.length > 0 ? (
        <ul className="m-0 list-none p-0">
          {extended.map((t) => (
            <li key={t.slug} className="flex flex-col items-start gap-x-3 gap-y-2 border-b border-rule py-2 last:border-b-0 md:flex-row md:items-center">
              <span className="min-w-0 flex-1">
                {t.name} · More time until {when(t.deadline_override)}
              </span>
              {!archived && (
                <Button variant="danger" disabled={!!lock} onClick={() => setClearing(t)}>
                  Clear this team’s extension
                </Button>
              )}
            </li>
          ))}
        </ul>
      ) : ev.submission_deadline && !archived ? (
        <p className="text-ink-soft">No team has an extension.</p>
      ) : null}
      {!archived &&
        (ev.submission_deadline ? (
          teams.length > 0 && (
            <form
              className="mt-3 grid gap-x-4 md:max-w-[44rem] md:grid-cols-2"
              noValidate
              onSubmit={(e) => {
                e.preventDefault();
                void give();
              }}
            >
              <Field id="ext-team" label="Team">
                <NativeSelect id="ext-team" className="w-full" value={team} disabled={!!lock} onChange={(e) => setTeam(e.target.value)}>
                  {teams.map((t) => (
                    <option key={t.slug} value={t.slug}>
                      {t.name}
                    </option>
                  ))}
                </NativeSelect>
              </Field>
              <Field id="ext-input" label={"More time until · " + (ev.time_zone || "UTC")} error={error}>
                <Input
                  id="ext-input"
                  type="datetime-local"
                  value={until}
                  disabled={!!lock}
                  aria-invalid={!!error || undefined}
                  aria-describedby={error ? "ext-input-msg" : undefined}
                  onChange={(e) => {
                    setUntil(e.target.value);
                    if (e.target.value) setError("");
                  }}
                />
              </Field>
              <div className="md:col-span-2">
                <Button type="submit" disabled={!!lock || busy}>
                  Give this team more time
                </Button>
              </div>
            </form>
          )
        ) : (
          <p className="text-sm text-ink-soft">Set the event deadline first. Then you can give one team more time.</p>
        ))}
      <AskDialog
        open={!!clearing}
        onOpenChange={(o) => !o && setClearing(null)}
        title="Clear the extension?"
        body={(clearing?.name || "This team") + " reverts to the event’s general deadline, which may already have passed. Clear the extension?"}
        confirmLabel="Clear extension"
        danger
        onConfirm={async () => {
          try {
            await api.teams.deadline(slug, clearing!.slug, { deadline: "" });
            await reload();
            toast("Saved");
          } catch (e) {
            toast(errText(e), { bad: true });
          }
        }}
      />
    </section>
  );
}
