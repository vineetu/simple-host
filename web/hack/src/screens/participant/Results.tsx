// Results (/e/{slug}/team?s=results, once published): the team's own place
// and score, its track win, the judges' anonymous comments, the standings, and
// the team's entry as the public sees it (or why it is not shown). With
// "Winners only", a team outside the winners sees a kind line, not its rank.
import { useQuery } from "@tanstack/react-query";
import { Badge } from "@kit/components/ui/badge";
import { Button } from "@kit/components/ui/button";
import { EmptyState } from "@kit/components/ui/empty-state";
import { PageTitle, Section } from "@kit/components/ui/page-shell";
import { Skeleton } from "@kit/components/ui/skeleton";
import type { ApiError } from "@kit/api/call";
import { useHack } from "@/lib/client";
import { safeHref } from "@/lib/labels";
import { useEventDetail } from "@/app/data";
import { Failed } from "@/app/states";
import type { ScreenProps } from "@/routes/registry";
import { EntryCard } from "@/shared/EntryCard";
import { decidingScore, headline, scoreText, type MyResults, type Standing, type Standings } from "./data";

const box = "mt-6 rounded-wobble border border-rule bg-card px-4 py-4 md:px-5";

function Out({ href, label, primary }: { href: string; label: string; primary?: boolean }) {
  return (
    <Button variant={primary ? "primary" : "secondary"} asChild>
      <a href={href} target="_blank" rel="noopener noreferrer">
        {label} <span aria-hidden="true">↗</span>
        <span className="sr-only"> (opens in a new tab)</span>
      </a>
    </Button>
  );
}

function Table({ rows, mine }: { rows: Standing[]; mine?: string }) {
  const hasRank = rows.some((r) => r.rank != null);
  const hasScores = rows.some((r) => r.raw_total != null || r.normalised_total != null);
  return (
    <Section title="Standings">
      <div className="rounded-wobble border border-rule bg-card px-3 py-1">
        <table className="w-full border-collapse text-left">
          <thead>
            <tr className="border-b border-rule text-sm text-ink-soft">
              {hasRank && <th className="w-16 py-2 pr-3 font-medium">Rank</th>}
              <th className="py-2 pr-3 font-medium">Team</th>
              {hasScores && <th className="py-2 text-right font-medium">Score</th>}
            </tr>
          </thead>
          <tbody>
            {rows.map((r, i) => {
              const s = decidingScore(r);
              return (
                <tr key={i} className="border-b border-rule last:border-b-0">
                  {hasRank && <td className="py-2.5 pr-3">{r.rank ?? ""}</td>}
                  <td className="py-2.5 pr-3">
                    {r.team_name} {r.team_name === mine && <Badge tone="accent">Your team</Badge>}
                  </td>
                  {hasScores && <td className="py-2.5 text-right tabular-nums">{s != null ? scoreText(s) : ""}</td>}
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </Section>
  );
}

export default function Results({ match }: ScreenProps) {
  const slug = match.slug!;
  const api = useHack();
  const d = useEventDetail(slug).data!;
  const teamName = d.me?.team?.name;
  const mineQ = useQuery({ queryKey: ["my-results", slug], queryFn: () => api.voting.myResults(slug).then((r) => r as MyResults) });
  const published = !!mineQ.data?.published;
  const standQ = useQuery({ queryKey: ["results", slug], enabled: published, queryFn: () => api.voting.results(slug).then((r) => r as Standings).catch(() => ({}) as Standings) });
  const eventPage = "/e/" + encodeURIComponent(slug);
  const title = <PageTitle sub={d.event.title}>Results</PageTitle>;
  if (mineQ.isPending || (published && standQ.isPending))
    return (
      <>
        {title}
        <div role="status" aria-busy="true" aria-label="Loading results">
          <Skeleton className="mt-6 h-40 w-full" />
          <Skeleton className="mt-6 h-48 w-full" />
        </div>
      </>
    );
  if (mineQ.isError) {
    const err = mineQ.error as ApiError;
    return (
      <>
        {title}
        {err.code === "no_team" ? (
          <EmptyState title="No result for you yet">Join or start a team to see a result once results are out.</EmptyState>
        ) : (
          <Failed error={err} retry={() => mineQ.refetch()} />
        )}
      </>
    );
  }
  const mine = mineQ.data;
  if (!mine.published)
    return (
      <>
        {title}
        <EmptyState title="Results haven't been published yet">They show here once the organisers publish them.</EmptyState>
      </>
    );
  const s = standQ.data || {};
  const winnersOnly = Array.isArray(s.winners);
  const rows = (winnersOnly ? s.winners : s.ranking) || [];
  const amWinner = mine.rank === 1 || mine.track_winner === true;
  if (winnersOnly && !amWinner)
    return (
      <>
        {title}
        <section className={box} aria-label="Your result">
          <p>
            {rows.length
              ? "Winners are posted. Your team isn't in the top placements this time — thanks for building with us."
              : "Results are posted. No teams were placed this time — thanks for building with us."}
          </p>
          <Button className="mt-3" asChild>
            <a href={eventPage}>Open the event</a>
          </Button>
        </section>
      </>
    );
  const ranked = typeof mine.ranked_teams === "number" ? mine.ranked_teams : rows.filter((r) => r.rank != null).length;
  const score = decidingScore(mine);
  const comments = (mine.comments || []).filter(Boolean);
  const e = mine.entry;
  const links = e
    ? (
        [
          [e.site_url, "Open website"],
          [e.video_url, "Watch video" + (e.video_host ? " · " + e.video_host : "")],
          [e.code_url, "View code" + (e.code_host ? " · " + e.code_host : "")],
        ] as const
      ).filter(([u]) => safeHref(u))
    : [];
  return (
    <>
      {title}
      <section className={box} aria-labelledby="mine-h">
        <h2 id="mine-h" className="type-card">
          {headline(mine, !winnersOnly && ranked ? ranked : null)}
        </h2>
        {score != null && <p className="text-ink-soft">Score {scoreText(score)}</p>}
        {mine.track_winner && mine.track && (
          <p className="mt-2">
            {mine.track.name} winner{mine.track.prize ? ": " + mine.track.prize : ""}.
          </p>
        )}
        {comments.length > 0 && (
          <>
            <h3 className="mt-4 type-section">Judges' anonymous comments</h3>
            <ul className="m-0 mt-1 list-none p-0">
              {comments.map((c, i) => (
                <li key={i} className="mt-2 max-w-[70ch]">
                  “{c}”
                </li>
              ))}
            </ul>
          </>
        )}
        <Button className="mt-4" asChild>
          <a href={eventPage}>See it on the event page</a>
        </Button>
      </section>
      {mine.shown_publicly && e ? (
        <Section title="Your entry as the public sees it">
          <EntryCard entry={{ ...e, team_name: teamName }} compact>
            {links.length > 0 && (
              <div className="mt-3 flex flex-wrap gap-2">
                {links.map(([u, label], i) => (
                  <Out key={label} href={safeHref(u)} label={label} primary={i === 0 && label === "Open website"} />
                ))}
              </div>
            )}
          </EntryCard>
        </Section>
      ) : (
        <p className="mt-6 text-ink-soft">
          {mine.rank != null ? "Only the winners are shown publicly — your entry itself is not public." : "Your entry isn't part of the published results yet."}
        </p>
      )}
      {rows.length > 0 && <Table rows={rows} mine={teamName} />}
    </>
  );
}
