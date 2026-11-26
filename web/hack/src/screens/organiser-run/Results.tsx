// Results (/e/{slug}/manage?s=results): whether results are published (and
// whether scores changed since), the live standings on request, and the six
// downloads. Publishing itself happens on Home, as before.
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Button } from "@kit/components/ui/button";
import { Notice } from "@kit/components/ui/notice";
import { PageTitle, Section } from "@kit/components/ui/page-shell";
import { Skeleton } from "@kit/components/ui/skeleton";
import { paths, useHack } from "@/lib/client";
import type { CsvExport } from "@/lib/client/exports";
import { dateTime } from "@/lib/time";
import { href } from "@/routes/resolve";
import { useEventDetail } from "@/app/data";
import { AppLink } from "@/app/nav";
import { Failed } from "@/app/states";
import type { ScreenProps } from "@/routes/registry";
import { RunFrame, errText, useDownload } from "./common";
import { publishedRows, rankText, scoreText, scoresChangedSincePublish, type Standing } from "./data-overview";

const DOWNLOADS: [CsvExport | "sites", string, string][] = [
  ["results", "Results (CSV)", "-results.csv"],
  ["scores", "Scores (CSV)", "-scores.csv"],
  ["entries", "Entries (CSV)", "-entries.csv"],
  ["teams", "Teams (CSV)", "-teams.csv"],
  ["participants", "People (CSV)", "-participants.csv"],
  ["sites", "All team sites (.tar.gz)", "-sites.tar.gz"],
];

/** zoned is the time with the event's zone name ("Fri 20 Nov, 6:00 pm WIB"). */
function zoned(iso: string | undefined, tz?: string): string {
  const base = dateTime(iso, { time_zone: tz });
  if (!base || !iso) return base;
  let name = "";
  try {
    name = new Intl.DateTimeFormat("en-US", { timeZone: tz || "UTC", timeZoneName: "short" }).formatToParts(new Date(iso)).find((p) => p.type === "timeZoneName")?.value || "";
  } catch {
    /* unknown zone: no label */
  }
  return name ? base + " " + name : base;
}

function StandingsTable({ rows }: { rows: Standing[] }) {
  if (!rows.length) return <p className="text-ink-soft">No team has a score yet.</p>;
  return (
    <table className="w-full border-collapse text-[15px] tabular-nums" aria-label="Current standings">
      <thead>
        <tr>
          {["Rank", "Team", "Score"].map((h) => (
            <th key={h} className="border-b border-rule bg-paper-lo px-2 py-1 text-left type-label text-ink-soft first:pl-3">
              {h}
            </th>
          ))}
        </tr>
      </thead>
      <tbody>
        {rows.map((r) => (
          <tr key={r.team_id}>
            <td className="border-b border-rule px-2 py-2 pl-3">{rankText(r)}</td>
            <td className="border-b border-rule px-2 py-2">{r.team_name || "A team"}</td>
            <td className="border-b border-rule px-2 py-2">{scoreText(r)}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function Preview({ slug }: { slug: string }) {
  const api = useHack();
  const [open, setOpen] = useState(false);
  const q = useQuery({ queryKey: ["standings-preview", slug], queryFn: () => api.judging.preview(slug), enabled: open });
  return (
    <details className="mt-6 rounded-wobble border border-rule bg-card px-4 md:px-5" onToggle={(e) => setOpen(e.currentTarget.open)}>
      <summary className="flex min-h-12 cursor-pointer items-center type-section">Preview current standings</summary>
      <div className="pb-4">
        {!open ? null : q.isPending ? (
          <p className="text-ink-soft">Loading…</p>
        ) : q.isError ? (
          <p className="text-bad" role="alert">
            {errText(q.error)}
          </p>
        ) : (
          <StandingsTable rows={((q.data as { results?: Standing[] }).results || []) as Standing[]} />
        )}
      </div>
    </details>
  );
}

export default function Results({ match }: ScreenProps) {
  const slug = match.slug!;
  const api = useHack();
  const ev = useEventDetail(slug).data!.event;
  const download = useDownload();
  const [busy, setBusy] = useState("");
  const q = useQuery({
    queryKey: ["results-screen", slug],
    queryFn: async () => {
      const [pub, prev] = await Promise.all([api.voting.results(slug).catch(() => null), api.judging.preview(slug).then((r) => ({ ok: true as const, r }), () => ({ ok: false as const, r: null }))]);
      return { results: publishedRows(pub), preview: ((prev.r as { results?: Standing[] } | null)?.results || []) as Standing[], previewOk: prev.ok };
    },
  });
  const title = <PageTitle sub="Preview and share your standings.">Results</PageTitle>;
  if (q.isPending)
    return (
      <RunFrame slug={slug} screen={match.id}>
        {title}
        <div role="status" aria-busy="true" aria-label="Loading results" className="mt-6">
          <Skeleton className="h-16 w-full" />
          <Skeleton className="mt-4 h-12 w-full" />
          <Skeleton className="mt-4 h-28 w-full" />
        </div>
      </RunFrame>
    );
  if (q.isError)
    return (
      <RunFrame slug={slug} screen={match.id}>
        {title}
        <Failed error={q.error} retry={() => q.refetch()} />
      </RunFrame>
    );
  const { results, preview, previewOk } = q.data;
  const published = !!results?.length;
  const stale = scoresChangedSincePublish(results, preview, previewOk);
  const when = published ? zoned(ev.results_published_at as string | undefined, ev.time_zone) : "";
  const secondary = "Publishing takes a snapshot. Publish again if scores change.";
  const home = href("organiser", slug, "overview");

  return (
    <RunFrame slug={slug} screen={match.id}>
      {title}
      {published ? (
        <Notice title={when ? "Published " + when : "Published"}>{secondary}</Notice>
      ) : (
        <Notice tone="bad" title="Not published yet">
          {secondary}
        </Notice>
      )}
      {!published && (
        <p>
          Publish from <AppLink to={home}>Home</AppLink> when ready.
        </p>
      )}
      {stale && (
        <p className="mt-2 text-sm text-ink-soft">
          Scores have changed since you published. Publish again from <AppLink to={home}>Home</AppLink>.
        </p>
      )}
      <Preview slug={slug} />
      <Section title="Downloads" className="mt-7">
        <div className="flex flex-wrap gap-2.5">
          {DOWNLOADS.map(([what, label, suffix]) => (
            <Button
              key={what}
              disabled={busy === what}
              onClick={async () => {
                setBusy(what);
                await download(what === "sites" ? paths.projects(slug) : paths.csv(slug, what), slug + suffix);
                setBusy("");
              }}
            >
              {label}
            </Button>
          ))}
        </div>
      </Section>
      <p className="mt-6">
        <AppLink to={href("organiser", slug, "settings") + "&tab=results"} className="inline-flex min-h-11 items-center">
          Results settings
        </AppLink>
      </p>
    </RunFrame>
  );
}
