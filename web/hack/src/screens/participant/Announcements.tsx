// Announcements (/e/{slug}/team?s=announcements): news from the organisers,
// pinned first, then newest. Opening it marks them seen in this browser, so
// the rail's unread count clears (sh_ann_seen:<slug>, as before).
import { useEffect } from "react";
import { useQuery } from "@tanstack/react-query";
import { EmptyState } from "@kit/components/ui/empty-state";
import { PageTitle } from "@kit/components/ui/page-shell";
import { Skeleton } from "@kit/components/ui/skeleton";
import { useHack } from "@/lib/client";
import { useEventDetail } from "@/app/data";
import { Failed } from "@/app/states";
import type { ScreenProps } from "@/routes/registry";
import { markAnnouncementsSeen, type Announcement } from "./data";
import { AnnouncementBody } from "./parts";

export default function Announcements({ match }: ScreenProps) {
  const slug = match.slug!;
  const api = useHack();
  const ev = useEventDetail(slug).data!.event;
  const q = useQuery({ queryKey: ["announcements", slug], queryFn: () => api.messages.announcements(slug).then((r) => (r.announcements as Announcement[]) || []) });
  useEffect(() => {
    if (q.data) markAnnouncementsSeen(slug, q.data);
  }, [q.data, slug]);
  const title = <PageTitle sub="News from the organisers.">Announcements</PageTitle>;
  if (q.isPending)
    return (
      <>
        {title}
        <div className="mt-7" role="status" aria-busy="true" aria-label="Loading announcements">
          <Skeleton className="h-36 w-full" />
          <Skeleton className="mt-3 h-36 w-full" />
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
  if (!q.data.length)
    return (
      <>
        {title}
        <EmptyState title="No announcements yet">News from the organisers shows here.</EmptyState>
      </>
    );
  return (
    <>
      {title}
      <div className="mt-7 flex flex-col gap-4">
        {q.data.map((a) => (
          <article key={a.id} className="rounded-wobble border border-rule bg-card px-4 py-4 md:px-5">
            <AnnouncementBody a={a} ev={ev} level="h2" />
          </article>
        ))}
      </div>
    </>
  );
}
