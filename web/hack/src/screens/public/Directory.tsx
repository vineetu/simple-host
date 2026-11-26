// Find a hackathon (/directory): public events, happening now, upcoming and
// past, each with its dates and, while it takes applications, Apply.
// (The old scripted static page hack-directory.html, on the kit.)
import { useQuery } from "@tanstack/react-query";
import { Button } from "@kit/components/ui/button";
import { PageTitle, Section } from "@kit/components/ui/page-shell";
import { Skeleton } from "@kit/components/ui/skeleton";
import { useHack } from "@/lib/client";
import type { DirectoryRow } from "@/lib/client/hack";
import { apiKey } from "@/lib/session";
import { EventMark } from "@/app/marks";
import { Failed } from "@/app/states";
import { dateRange } from "./directory";
import { PublicFoot } from "./PublicFoot";

function Row({ e }: { e: DirectoryRow }) {
  const sub = [e.tagline, dateRange(e.starts_at, e.ends_at, e.time_zone)].filter(Boolean).join(" · ");
  return (
    <li className="flex min-h-14 items-center gap-3 border-b border-rule px-1 py-2 last:border-b-0">
      {e.icon_url ? <img src={e.icon_url} alt="" className="size-8 flex-none rounded-sm object-cover" /> : <EventMark title={e.title} color={e.color} />}
      <a href={e.url} className="flex min-h-11 min-w-0 flex-1 flex-col justify-center no-underline">
        <span className="font-semibold text-ink">{e.title}</span>
        {sub && <span className="text-sm text-ink-soft">{sub}</span>}
      </a>
      {e.applications_open && e.apply_url && (
        <Button asChild>
          <a href={e.apply_url} aria-label={"Apply to " + e.title}>
            Apply
          </a>
        </Button>
      )}
    </li>
  );
}

const GROUPS = [
  ["now", "Happening now"],
  ["upcoming", "Upcoming"],
  ["past", "Past"],
] as const;

export default function Directory() {
  const api = useHack();
  const q = useQuery({ queryKey: ["directory"], queryFn: () => api.hack.directoryGroups() });
  return (
    <>
      <PageTitle sub="Public events on Simple Hack, from those happening now to past entry showcases.">Find a hackathon</PageTitle>
      <p className="mt-3 flex flex-wrap gap-x-5">
        <a href="/events/new" className="inline-flex min-h-11 items-center">
          Create an event
        </a>
        {!apiKey() && (
          <a href="/signin" className="inline-flex min-h-11 items-center">
            Sign in
          </a>
        )}
      </p>
      {q.isPending ? (
        <div className="mt-6" role="status" aria-busy="true" aria-label="Loading events">
          <Skeleton className="h-16 w-full" />
          <Skeleton className="mt-2 h-16 w-full" />
        </div>
      ) : q.isError ? (
        <Failed error={new Error("Could not load events. Try again soon.")} retry={() => q.refetch()} />
      ) : (
        GROUPS.map(([key, label]) => {
          const rows = q.data[key] || [];
          return (
            <Section key={key} title={label} hideTitle={false}>
              {rows.length ? (
                <ul aria-label={label} className="m-0 list-none rounded-wobble border border-rule bg-card px-3 py-1">
                  {rows.map((e) => (
                    <Row key={e.url} e={e} />
                  ))}
                </ul>
              ) : (
                <p className="text-ink-soft">Nothing here yet.</p>
              )}
            </Section>
          );
        })
      )}
      <PublicFoot />
    </>
  );
}
