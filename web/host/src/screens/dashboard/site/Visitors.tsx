// Visitors: views and people per day for 7, 30 or 90 days, people against
// bots, and the top pages, referrers and countries.
import { useState } from "react";
import { num } from "@kit/lib/format";
import { Chips } from "@kit/components/ui/badge";
import { Bars } from "@kit/components/ui/bars";
import { Button } from "@kit/components/ui/button";
import { EmptyState } from "@kit/components/ui/empty-state";
import { Skeleton } from "@kit/components/ui/skeleton";
import { Stat, Stats } from "@kit/components/ui/stat";
import { useStats, type Count, type Site, type Stats as StatsData } from "../data";

const RANGES = [
  ["7", "7 days"],
  ["30", "30 days"],
  ["90", "90 days"],
] as const;

export function Visitors({ site }: { site: Site }) {
  const [days, setDays] = useState<"7" | "30" | "90">("30");
  const q = useStats(site, Number(days));
  const st = q.data;
  return (
    <>
      <div className="flex flex-wrap items-center justify-between gap-x-4">
        <h2 className="type-section">Visitors</h2>
        <Chips label="Period" options={RANGES} value={days} onChange={setDays} />
      </div>
      {q.isPending ? (
        <div role="status" aria-label="Loading visitor numbers">
          <Skeleton className="mt-2 h-20" />
          <Skeleton className="mt-4 h-24" />
        </div>
      ) : q.isError ? (
        <EmptyState title="Could not load visitor numbers" action={<Button onClick={() => q.refetch()}>Try again</Button>}>
          {(q.error as Error).message}
        </EmptyState>
      ) : (
        <>
          <Stats>
            <Stat value={num(st!.views)} label="Page views" />
            <Stat value={num(st!.people)} label="People" />
            <Stat value={num(st!.bots)} label="Bot visits, left out" />
          </Stats>
          {st!.views ? (
            <Bars
              days={fill(st!.days, Number(days))}
              label={num(st!.views) + " page views in the last " + days + " days, by day"}
            />
          ) : (
            <p className="my-4 text-ink-soft">No visits in the last {days} days yet.</p>
          )}
          <p className="text-sm text-ink-soft">Your own visits and bots are left out. New visits show within a minute.</p>
          <div className="mt-6 grid gap-x-8 gap-y-6 md:grid-cols-3">
            <Top title="Pages" rows={st!.pages} none="No pages viewed yet." />
            <Top title="Came from" rows={st!.referrers} none="No other sites linked here yet." />
            <Top title="Countries" rows={st!.countries} none="No countries yet." />
          </div>
        </>
      )}
    </>
  );
}

function Top({ title, rows, none }: { title: string; rows: Count[]; none: string }) {
  const max = Math.max(1, ...rows.map((r) => r.n));
  return (
    <section>
      <h3 className="type-label mb-1.5 text-ink-soft">{title}</h3>
      {rows.length ? (
        <ol className="m-0 list-none p-0">
          {rows.slice(0, 8).map((r) => (
            <li key={r.key} className="relative flex items-baseline justify-between gap-3 border-b border-rule py-1.5 text-sm">
              <span aria-hidden="true" className="absolute inset-y-1 left-0 rounded-sm bg-accent-soft" style={{ width: (r.n / max) * 100 + "%" }} />
              <span className="relative min-w-0 pl-1.5 break-all">{r.key || "(direct)"}</span>
              <span className="relative pr-1 tabular-nums">{num(r.n)}</span>
            </li>
          ))}
        </ol>
      ) : (
        <p className="text-sm text-ink-soft">{none}</p>
      )}
    </section>
  );
}

/** fill is one bar per day of the period, ending today (UTC); days with no visits count 0. */
function fill(list: StatsData["days"], n: number) {
  const have = new Map(list.map((d) => [d.day, d.views]));
  const today = Date.now();
  return Array.from({ length: n }, (_, i) => {
    const day = new Date(today - (n - 1 - i) * 86400000).toISOString().slice(0, 10);
    return { day, n: have.get(day) || 0 };
  });
}
