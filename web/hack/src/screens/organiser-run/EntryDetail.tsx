// Entry detail (/e/{slug}/manage?s=entry&team=<slug>): one team's entry as
// the judges read it (the shared summary card), with its status above it
// (No entry, Draft, Not complete, Submitted, Excluded), Give more time (to
// Extensions, the team picked), Exclude or Include in judging, the links
// and screenshot, and the team's site: open it, take it down with a reason,
// or put it back (not when Simple Host took it down).
import { useState, type ReactNode } from "react";
import { useLocation } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { Badge } from "@kit/components/ui/badge";
import { Button } from "@kit/components/ui/button";
import { EmptyState } from "@kit/components/ui/empty-state";
import { Label, Textarea } from "@kit/components/ui/input";
import { Notice } from "@kit/components/ui/notice";
import { PageTitle, Section } from "@kit/components/ui/page-shell";
import { Skeleton } from "@kit/components/ui/skeleton";
import { useToast } from "@kit/components/ui/toast";
import { ApiError } from "@kit/api/call";
import { paths, useHack, type EventDetail } from "@/lib/client";
import { possessive, safeHref } from "@/lib/labels";
import { dateTime } from "@/lib/time";
import { useEventDetail } from "@/app/data";
import { AppLink } from "@/app/nav";
import { Failed } from "@/app/states";
import type { ScreenProps } from "@/routes/registry";
import { EntryCard } from "@/shared/EntryCard";
import { Screenshot } from "@/shared/Screenshot";
import { errText, LockNote, RunFrame, useRefreshEvent, writeLock } from "./common";
import { entriesHref, joinAnd, labelList, type EntriesList, type EntryItem, type EntryRules, type RunTeam, type TeamsList } from "./data-entries";

export default function EntryDetail({ match }: ScreenProps) {
  const slug = match.slug!;
  return (
    <RunFrame slug={slug} screen="organiser-entry-detail">
      <Body slug={slug} />
    </RunFrame>
  );
}

function Crumbs({ slug, name }: { slug: string; name?: string }) {
  return (
    <nav aria-label="Breadcrumb" className="mt-4 text-sm text-ink-soft">
      <AppLink to={entriesHref(slug)} className="inline-flex min-h-11 items-center">
        Entries
      </AppLink>
      {name ? " / " + name : ""}
    </nav>
  );
}

function Body({ slug }: { slug: string }) {
  const api = useHack();
  const d = useEventDetail(slug).data!;
  const team = new URLSearchParams(useLocation().search).get("team") || "";
  const teams = useQuery({ queryKey: ["teams", slug], queryFn: () => api.teams.list(slug) as Promise<TeamsList> });
  const entries = useQuery({ queryKey: ["entries", slug], queryFn: () => api.entries.list(slug) as Promise<EntriesList> });
  const rules = useQuery({ queryKey: ["entry-rules", slug], queryFn: () => api.entries.rules(slug) as Promise<EntryRules> });

  if (teams.isPending || entries.isPending)
    return (
      <>
        <Crumbs slug={slug} />
        <div className="mt-4" role="status" aria-busy="true" aria-label="Loading the entry">
          <Skeleton className="h-8 w-56" />
          <Skeleton className="mt-6 h-20 w-full" />
          <Skeleton className="mt-4 h-64 w-full" />
        </div>
      </>
    );
  if (teams.isError)
    return (
      <>
        <Crumbs slug={slug} />
        <Failed error={teams.error} retry={() => teams.refetch()} />
      </>
    );
  const t = (teams.data.teams || []).find((x) => x.slug === team);
  if (!t)
    return (
      <>
        <Crumbs slug={slug} />
        <EmptyState
          title="Team not found."
          action={
            <Button asChild>
              <AppLink to={entriesHref(slug)}>All entries</AppLink>
            </Button>
          }
        >
          It may have been deleted, or the link is wrong.
        </EmptyState>
      </>
    );
  const entry = (entries.data?.entries || []).find((e) => e.team.slug === team) || null;
  return <Loaded slug={slug} d={d} team={t} entry={entry} rules={rules.data} />;
}

function Status({ slug, d, team, entry, lock, onInclude }: { slug: string; d: EventDetail; team: RunTeam; entry: EntryItem | null; lock: string; onInclude: (include: boolean) => void }) {
  const ev = d.event;
  const missing = entry?.missing || [];
  const more = lock ? (
    <Button disabled>Give more time</Button>
  ) : (
    <Button asChild>
      <AppLink to={entriesHref(slug, team.slug)}>Give more time</AppLink>
    </Button>
  );
  const box = (title: string, line: string, tone: "plain" | "warn" | "good", actions?: ReactNode) => (
    <div
      className={
        "mt-5 flex flex-wrap items-center gap-x-4 gap-y-3 rounded-field border-[1.5px] px-4 py-3 " +
        (tone === "warn" ? "border-accent bg-accent-soft" : tone === "good" ? "border-good bg-card" : "border-rule bg-card")
      }
      aria-label="Entry status"
      role="group"
    >
      <div className="min-w-0 flex-[1_1_16rem]">
        <p className="font-semibold">{title}</p>
        {line && <p className="mt-0.5 text-sm text-ink-soft">{line}</p>}
      </div>
      {actions && <div className="flex min-w-0 flex-wrap gap-2">{actions}</div>}
    </div>
  );
  if (!entry) return box("No entry", "This team hasn’t started one.", "warn", more);
  if (entry.excluded)
    return box(
      "Excluded from judging",
      "You excluded this entry by hand. Judges won’t see it.",
      "warn",
      <Button disabled={!!lock} onClick={() => onInclude(true)}>
        Include in judging
      </Button>,
    );
  if (entry.incomplete)
    return box(
      "Not complete",
      "It will still be judged, marked incomplete." + (missing.length ? " Missing: " + joinAnd(labelList(missing)) + "." : ""),
      "warn",
      <>
        {more}
        <Button variant="danger" disabled={!!lock} onClick={() => onInclude(false)}>
          Exclude from judging
        </Button>
      </>,
    );
  if (entry.status === "submitted")
    return box(
      "Submitted" + (entry.submitted_at ? " · " + dateTime(entry.submitted_at, ev) : ""),
      missing.length
        ? `The organisers now also ask for ${joinAnd(labelList(missing))}. It stays submitted.`
        : ["judging", "results", "archived"].includes(ev.stage)
          ? "Judges can see this."
          : "Judges see this once judging starts.",
      "good",
    );
  const deadline = team.deadline ? "Deadline" + (team.extended ? " with their extension" : "") + ": " + dateTime(team.deadline, ev) + "." : "";
  const miss = missing.length ? "Missing: " + joinAnd(labelList(missing)) + ". " : "";
  return box("Draft · not submitted", (miss + deadline).trim(), "plain", more);
}

function LinkRow({ label, value, off }: { label: string; value?: string; off?: boolean }) {
  const link = safeHref(value);
  return (
    <>
      <dt className="text-sm text-ink-soft">{label}</dt>
      <dd className="m-0 min-w-0">
        {off ? (
          "Off for this event"
        ) : link ? (
          <a href={link} target="_blank" rel="noopener noreferrer" className="inline-flex min-h-11 min-w-0 items-center [overflow-wrap:anywhere]">
            {value}
            <span className="sr-only"> (opens in a new tab)</span>
          </a>
        ) : value ? (
          value
        ) : (
          <span className="text-ink-faint">(none)</span>
        )}
      </dd>
    </>
  );
}

function Loaded({ slug, d, team, entry, rules }: { slug: string; d: EventDetail; team: RunTeam; entry: EntryItem | null; rules?: EntryRules }) {
  const api = useHack();
  const toast = useToast();
  const refresh = useRefreshEvent(slug);
  const lock = writeLock(d);
  const [reason, setReason] = useState("");
  const [platform, setPlatform] = useState(false);
  const [busy, setBusy] = useState(false);
  const reload = () => refresh(["entries", slug], ["teams", slug]);
  const site = team.site;
  const siteUrl = safeHref(site?.url);

  const setIncluded = async (include: boolean, undoable = true) => {
    try {
      await api.entries.exclude(slug, team.slug, !include);
    } catch (e) {
      toast(errText(e), { bad: true });
      return;
    }
    await reload();
    toast(include ? "Included in judging." : "Excluded from judging. Judges stop seeing it; the team still sees its own entry.", {
      undo: undoable ? { label: "Undo", run: () => void setIncluded(!include, false) } : undefined,
    });
  };

  const takeDown = async (why: string, undoable = true) => {
    setBusy(true);
    try {
      await api.teams.takeDown(slug, team.slug, { reason: why });
      await reload();
      setReason("");
      toast("Site taken down. Visitors see a notice instead of the site.", { undo: undoable ? { label: "Undo", run: () => void putBack(false) } : undefined });
    } catch (e) {
      toast(errText(e), { bad: true });
    } finally {
      setBusy(false);
    }
  };

  const putBack = async (undoable = true, why = "") => {
    setBusy(true);
    try {
      await api.teams.restore(slug, team.slug);
      await reload();
      toast("Site put back.", { undo: undoable ? { label: "Undo", run: () => void takeDown(why, false) } : undefined });
    } catch (e) {
      if (e instanceof ApiError && e.code === "platform_takedown") setPlatform(true);
      else toast(errText(e), { bad: true });
    } finally {
      setBusy(false);
    }
  };

  const names = (team.members || [])
    .map((m) => m.display_name)
    .filter(Boolean)
    .join(", ");

  return (
    <>
      <Crumbs slug={slug} name={team.name} />
      <PageTitle sub={(team.name ? possessive(team.name) + " " : "This team's ") + "entry and site."}>{team.name}</PageTitle>
      <LockNote reason={lock} />
      <Status slug={slug} d={d} team={team} entry={entry} lock={lock} onInclude={(inc) => void setIncluded(inc)} />

      {entry ? (
        <>
          <div className="mt-6">
            <EntryCard
              entry={{ ...entry, team_name: team.name, team_members: names }}
              badges={entry.incomplete && !entry.excluded ? <Badge tone="accent">Incomplete</Badge> : undefined}
            />
          </div>
          <Section title="Show your work">
            <dl className="grid grid-cols-1 gap-x-4 gap-y-1 md:grid-cols-[8rem_minmax(0,1fr)] md:gap-y-2">
              <LinkRow label="Video link" value={entry.video_url} />
              <LinkRow label="Code link" value={entry.code_url} off={rules?.fields?.code_url === "off"} />
            </dl>
            <h3 className="mt-4 type-label text-ink-soft">Screenshot</h3>
            {entry.has_screenshot ? (
              <div className="md:max-w-card">
                <Screenshot path={paths.teamScreenshot(slug, team.slug)} alt="Entry screenshot" />
              </div>
            ) : (
              <p className="text-sm text-ink-soft">No screenshot.</p>
            )}
          </Section>
        </>
      ) : (
        <Section title="Entry">
          <p className="text-ink-soft">No entry yet.</p>
        </Section>
      )}

      <Section title="Site">
        {!site?.exists ? (
          <p>No site published yet.</p>
        ) : (
          <>
            <p className="flex flex-wrap items-center gap-2">
              {siteUrl ? (
                <a href={siteUrl} target="_blank" rel="noopener noreferrer" className="inline-flex min-h-11 min-w-0 items-center font-mono text-sm [overflow-wrap:anywhere]">
                  {site.url}
                  <span className="sr-only"> (opens in a new tab)</span>
                </a>
              ) : (
                <span className="font-mono text-sm">{site.url}</span>
              )}
              {!site.taken_down && <Badge tone="ok">Live</Badge>}
            </p>
            {site.taken_down ? (
              platform ? (
                <Notice title="Taken down by Simple Host.">You can’t restore this one.</Notice>
              ) : (
                <>
                  <Notice title="Taken down by you">Reason: {site.taken_down_reason || "No reason given."}</Notice>
                  <Button disabled={!!lock || busy} onClick={() => void putBack(true, site.taken_down_reason || "")}>
                    Put back
                  </Button>
                </>
              )
            ) : (
              siteUrl && (
                <Button asChild className="mt-2">
                  <a href={siteUrl} target="_blank" rel="noopener noreferrer">
                    Open site <span aria-hidden="true">↗</span>
                    <span className="sr-only"> (opens in a new tab)</span>
                  </a>
                </Button>
              )
            )}
          </>
        )}
      </Section>

      {site?.exists && !site.taken_down && (
        <Section title="Danger zone" className="rounded-field border border-bad px-4 py-3">
          <p className="font-semibold">Take this site down</p>
          <p className="text-sm text-ink-soft">Visitors see a notice instead of the site. The team can see why if you add a reason. You can put it back.</p>
          <div className="mt-3 md:max-w-card">
            <Label htmlFor="takedown-reason">Reason shown to the team (optional)</Label>
            <Textarea id="takedown-reason" maxLength={300} value={reason} disabled={!!lock} onChange={(e) => setReason(e.target.value)} className="min-h-20" />
          </div>
          <Button variant="danger" className="mt-3" disabled={!!lock || busy} onClick={() => void takeDown(reason.trim())}>
            Take this site down
          </Button>
        </Section>
      )}

      {entry?.description && (
        <details className="mt-7 rounded-wobble border border-rule bg-card px-4 py-2">
          <summary className="flex min-h-11 cursor-pointer items-center type-section">More detail from the team</summary>
          <p className="pb-2 whitespace-pre-line">{entry.description}</p>
        </details>
      )}
    </>
  );
}
