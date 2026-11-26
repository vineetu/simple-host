// Schedule (/e/{slug}/team?s=schedule): the rows the organisers typed and the
// event's real times (start, end, the entry deadline or this team's own
// extension, voting, results) in one list, in time order, with Now and Next.
// A row starting within 15 minutes, or on now, also shows as one line at the
// top, the same line Home shows.
import { useQuery } from "@tanstack/react-query";
import { Badge } from "@kit/components/ui/badge";
import { EmptyState } from "@kit/components/ui/empty-state";
import { PageTitle } from "@kit/components/ui/page-shell";
import { Skeleton } from "@kit/components/ui/skeleton";
import { useHack } from "@/lib/client";
import { useEventDetail } from "@/app/data";
import { Failed } from "@/app/states";
import type { ScreenProps } from "@/routes/registry";
import { label, merge, whenText, type Content } from "./data";
import { Nudge, useNow } from "./parts";

export default function Schedule({ match }: ScreenProps) {
  const slug = match.slug!;
  const api = useHack();
  const ev = useEventDetail(slug).data!.event;
  const q = useQuery({ queryKey: ["content", slug], queryFn: () => api.content.get(slug).then((r) => r as Content) });
  const now = useNow(30000);
  const title = <PageTitle sub={ev.time_zone ? `Times are in ${ev.time_zone}.` : undefined}>Schedule</PageTitle>;
  if (q.isPending)
    return (
      <>
        {title}
        <div className="mt-7" role="status" aria-busy="true" aria-label="Loading the schedule">
          <Skeleton className="h-14 w-full" />
          <Skeleton className="mt-2 h-14 w-full" />
          <Skeleton className="mt-2 h-14 w-full" />
        </div>
      </>
    );
  if (q.isError)
    return (
      <>
        {title}
        <Failed error={q.error} retry={() => q.refetch()} />
      </>
    );
  const rows = label(merge(q.data, ev), now);
  if (!rows.length)
    return (
      <>
        {title}
        <EmptyState title="Nothing scheduled yet">Times show here once the organisers add them.</EmptyState>
      </>
    );
  return (
    <>
      {title}
      <Nudge rows={rows} ev={ev} />
      <ul aria-label="Schedule" className="m-0 mt-6 list-none rounded-wobble border border-rule bg-card px-4 py-1">
        {rows.map((r, i) => (
          <li key={i} className="flex flex-col gap-x-4 gap-y-0.5 border-b border-rule py-3 last:border-b-0 md:flex-row md:items-baseline">
            <span className="min-w-0 flex-1">
              <span className="flex flex-wrap items-center gap-2 font-semibold text-ink">
                {r.title}
                {r.now ? <Badge tone="accent">Now</Badge> : r.next ? <Badge>Next</Badge> : null}
              </span>
              {r.description && <span className="block text-sm text-ink-soft">{r.description}</span>}
            </span>
            <span className="text-sm text-ink-soft md:flex-none md:text-right">{whenText(r, ev)}</span>
          </li>
        ))}
      </ul>
    </>
  );
}
