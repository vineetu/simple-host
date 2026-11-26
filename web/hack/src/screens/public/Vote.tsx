// People's choice (/e/{slug}/vote): anyone can see the teams; a signed-in
// person who may vote picks one and can change it until voting closes; once
// closed, the final votes. (The old scripted static page hack-voting.html.)
// The ballot is public; whether this person may vote (and their current
// vote) is a second, signed-in read, and nothing is offered until it answers.
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Badge } from "@kit/components/ui/badge";
import { Button } from "@kit/components/ui/button";
import { EmptyState } from "@kit/components/ui/empty-state";
import { Notice } from "@kit/components/ui/notice";
import { PageTitle, Section } from "@kit/components/ui/page-shell";
import { Skeleton } from "@kit/components/ui/skeleton";
import { useToast } from "@kit/components/ui/toast";
import type { ApiError } from "@kit/api/call";
import { useHack } from "@/lib/client";
import { possessive, safeHref } from "@/lib/labels";
import { apiKey } from "@/lib/session";
import { zonedDate } from "@/lib/time";
import type { ScreenProps } from "@/routes/registry";
import { ineligible, intro } from "./vote";
import { PublicFoot } from "./PublicFoot";

export default function Vote({ match }: ScreenProps) {
  const slug = match.slug!;
  const api = useHack();
  const qc = useQueryClient();
  const toast = useToast();
  const signedIn = !!apiKey();
  const [busy, setBusy] = useState("");
  const ballot = useQuery({ queryKey: ["ballot", slug], queryFn: () => api.voting.ballotOf(slug) });
  const status = ballot.data?.status || "";
  const mine = useQuery({
    queryKey: ["my-vote", slug],
    queryFn: () => api.voting.myVoteOf(slug),
    enabled: signedIn && (status === "open" || status === "closed"),
  });
  const back = (
    <p className="mt-4">
      <a href={"/e/" + encodeURIComponent(slug)} className="inline-flex min-h-11 items-center">
        ← Back to event
      </a>
    </p>
  );

  if (ballot.isPending)
    return (
      <>
        {back}
        <PageTitle>People’s choice</PageTitle>
        <div className="mt-6 grid gap-4 md:grid-cols-2" role="status" aria-busy="true" aria-label="Loading the vote">
          <Skeleton className="h-40" />
          <Skeleton className="h-40" />
        </div>
      </>
    );
  if (ballot.isError)
    return (
      <>
        {back}
        <PageTitle>People’s choice</PageTitle>
        <EmptyState title="Voting page unavailable" action={<Button onClick={() => ballot.refetch()}>Try again</Button>}>
          {(ballot.error as ApiError).message}
        </EmptyState>
        <PublicFoot find />
      </>
    );

  const d = ballot.data;
  const zone = d.time_zone || "UTC";
  const options = d.options || [];
  const known = mine.data;
  const eligible = known ? known.eligible !== false : null;
  const checkFailed = signedIn && mine.isError;
  const chosen = known?.team || "";
  const chosenName = options.find((o) => o.team === chosen)?.team_name || chosen;
  const signInHref = "/signin?next=" + encodeURIComponent(location.pathname);

  const vote = async (team: string) => {
    setBusy(team);
    try {
      const r = await api.voting.castVote(slug, team);
      await qc.invalidateQueries({ queryKey: ["my-vote", slug] });
      await qc.invalidateQueries({ queryKey: ["ballot", slug] });
      toast(r.changed ? "Vote changed." : "Vote saved.");
    } catch (e) {
      const err = e as ApiError;
      if (err.status !== 401)
        toast(err.code === "vote_ineligible" ? ineligible(d.eligibility) : err.code === "own_team_vote" ? "You can't vote for your own team." : err.message || "Could not save vote.", { bad: true });
    } finally {
      setBusy("");
    }
  };

  return (
    <>
      {back}
      <PageTitle sub={intro(status)}>{"People’s choice · " + d.title}</PageTitle>
      {d.opens_at && d.closes_at && (
        <p className="mt-3 text-ink-soft">
          Open {zonedDate(d.opens_at, zone)} to {zonedDate(d.closes_at, zone)}
        </p>
      )}
      {status === "open" && !signedIn && (
        <p className="mt-2 text-sm text-ink-soft">
          <a href={signInHref}>Sign in to vote</a>. You can change your vote until voting closes.
        </p>
      )}
      {status === "open" && signedIn && eligible === false && <Notice title={ineligible(d.eligibility)} />}
      {status === "open" && checkFailed && (
        <Notice tone="bad" title="Couldn't check whether you can vote." action={<Button onClick={() => mine.refetch()}>Retry</Button>} />
      )}
      {status === "open" && chosen && <p className="mt-3">Your vote: {chosenName}</p>}
      {status === "closed" && chosen && <p className="mt-3">You voted for {chosenName}</p>}
      {status === "open" && !options.length && eligible !== false && !checkFailed && (
        <EmptyState title="No teams to vote for yet">Teams appear here once their site is live.</EmptyState>
      )}
      {options.length > 0 && (
        <ul className="m-0 mt-6 grid list-none gap-4 p-0 md:grid-cols-2" aria-label="Teams">
          {options.map((o) => (
            <li key={o.team} className="min-w-0 rounded-wobble border-[1.5px] border-ink-soft bg-card px-5 py-4">
              <h2 className="type-section">{o.title}</h2>
              <p className="mt-0.5 text-sm text-ink-soft">{o.team_name}</p>
              {o.tagline && <p className="mt-1 text-ink-soft">{o.tagline}</p>}
              <div className="mt-3 flex flex-wrap items-center gap-2">
                {safeHref(o.url) && (
                  <Button asChild>
                    <a href={safeHref(o.url)} target="_blank" rel="noopener noreferrer">
                      View {possessive(o.team_name)} site <span aria-hidden="true">↗</span>
                      <span className="sr-only"> (opens in a new tab)</span>
                    </a>
                  </Button>
                )}
                {status === "open" &&
                  (!signedIn ? (
                    <Button asChild>
                      <a href={signInHref}>Sign in to vote</a>
                    </Button>
                  ) : known?.own_team === o.team ? (
                    <Badge>This is your team</Badge>
                  ) : eligible === true && chosen === o.team ? (
                    <Badge tone="ok">Your current vote</Badge>
                  ) : eligible === true ? (
                    <Button onClick={() => vote(o.team)} disabled={!!busy}>
                      Vote for this team<span className="sr-only">: {o.title}</span>
                    </Button>
                  ) : null)}
              </div>
            </li>
          ))}
        </ul>
      )}
      {status === "closed" && (
        <Section title="Final votes" hideTitle={false}>
          {(d.ranking || []).length ? (
            <ol className="m-0 list-none rounded-wobble border border-rule bg-card px-3 py-1">
              {(d.ranking || []).map((r, i) => (
                <li key={r.team} className="flex min-h-11 items-center gap-3 border-b border-rule px-1 last:border-b-0">
                  <span className="flex-1">
                    {i + 1}. {r.team_name}
                  </span>
                  <span className="text-ink">{r.votes + (r.votes === 1 ? " vote" : " votes")}</span>
                </li>
              ))}
            </ol>
          ) : (
            <p className="text-ink-soft">No votes were cast.</p>
          )}
        </Section>
      )}
      <PublicFoot find />
    </>
  );
}
