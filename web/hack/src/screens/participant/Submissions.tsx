// Submissions (/e/{slug}/team?s=submissions; judging, results and ended):
// every team's entry card, with its site's screenshot and its links. Before
// judging the server refuses (409 submissions_not_open), which is said plainly.
import { useQuery } from "@tanstack/react-query";
import { Badge } from "@kit/components/ui/badge";
import { Button } from "@kit/components/ui/button";
import { EmptyState } from "@kit/components/ui/empty-state";
import { PageTitle } from "@kit/components/ui/page-shell";
import { Skeleton } from "@kit/components/ui/skeleton";
import type { ApiError } from "@kit/api/call";
import { paths, useHack } from "@/lib/client";
import { safeHref } from "@/lib/labels";
import { useEventDetail } from "@/app/data";
import { Failed } from "@/app/states";
import type { ScreenProps } from "@/routes/registry";
import { EntryCard } from "@/shared/EntryCard";
import { Screenshot } from "@/shared/Screenshot";

export default function Submissions({ match }: ScreenProps) {
  const slug = match.slug!;
  const api = useHack();
  const mine = useEventDetail(slug).data?.me?.team?.slug;
  const q = useQuery({ queryKey: ["submissions", slug], queryFn: () => api.entries.submissions(slug) });
  const title = <PageTitle sub="Explore what the teams submitted.">Submissions</PageTitle>;

  if (q.isPending)
    return (
      <>
        {title}
        <div className="mt-7 grid gap-4 md:grid-cols-2" role="status" aria-busy="true" aria-label="Loading submissions">
          <Skeleton className="h-72" />
          <Skeleton className="h-72" />
        </div>
      </>
    );
  if (q.isError) {
    const err = q.error as ApiError;
    return (
      <>
        {title}
        {err.status === 409 || err.code === "submissions_not_open" ? (
          <EmptyState title="Not open yet">Submissions open to everyone when judging starts.</EmptyState>
        ) : (
          <Failed error={err} retry={() => q.refetch()} />
        )}
      </>
    );
  }
  if (!q.data.length)
    return (
      <>
        {title}
        <EmptyState title="No submissions to show yet">Teams’ entries appear here once they are submitted.</EmptyState>
      </>
    );
  return (
    <>
      {title}
      <ul className="m-0 mt-7 grid list-none gap-4 p-0 md:grid-cols-2" aria-label="Submissions">
        {q.data.map((s) => {
          const links = (
            [
              [s.site_url, "Open website"],
              [s.video_url, "Watch video"],
              [s.code_url, "View code"],
            ] as const
          ).filter(([u]) => safeHref(u));
          return (
            <li key={s.team} className="min-w-0">
              <EntryCard entry={s} compact badges={s.team === mine ? <Badge tone="accent">Your team</Badge> : undefined}>
                {s.has_screenshot && <Screenshot path={paths.submissionScreenshot(slug, s.team)} alt={"The website of " + s.team_name} />}
                {links.length > 0 && (
                  <div className="mt-3 flex flex-wrap gap-2">
                    {links.map(([u, label]) => (
                      <Button key={label} asChild>
                        <a href={safeHref(u)} target="_blank" rel="noopener noreferrer">
                          {label} <span aria-hidden="true">↗</span>
                          <span className="sr-only"> (opens in a new tab)</span>
                        </a>
                      </Button>
                    ))}
                  </div>
                )}
              </EntryCard>
            </li>
          );
        })}
      </ul>
    </>
  );
}
