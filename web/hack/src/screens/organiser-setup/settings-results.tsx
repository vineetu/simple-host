// Settings → Results: how teams are ranked, what the public page shows
// once results are published, and people's choice voting (window and who
// can vote). Publishing itself is on Home and Results.
import { useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError } from "@kit/api/call";
import { AskDialog } from "@kit/components/ui/dialog";
import { Badge } from "@kit/components/ui/badge";
import { Check } from "@kit/components/ui/choice";
import { Input, Label, NativeSelect } from "@kit/components/ui/input";
import { Notice } from "@kit/components/ui/notice";
import { useToast } from "@kit/components/ui/toast";
import { useHack } from "@/lib/client";
import { href } from "@/routes/resolve";
import { AppLink } from "@/app/nav";
import { Failed } from "@/app/states";
import type { TabProps } from "./Settings";
import { errorText, localInput, LockNote, Panel, useUnsavedGuard } from "./common";
import { locks, SaveRow, submit, TabLoading, useAnchor } from "./settings-ui";

type Scoring = { score_mode?: string; public_scores?: boolean; public_ranks?: boolean };
type Voting = { enabled?: boolean; opens_at?: string | null; closes_at?: string | null; eligibility?: string; status?: string; directory_listed?: boolean };
type Results = { results?: unknown[] | null; full_ranking?: boolean } | unknown[];

const STATUS: Record<string, [string, "ok" | "neutral" | "down"]> = {
  open: ["Voting open", "ok"],
  upcoming: ["Voting upcoming", "neutral"],
  closed: ["Voting closed", "neutral"],
  disabled: ["Voting off", "neutral"],
};

export function ResultsTab({ d }: TabProps) {
  const api = useHack();
  const qc = useQueryClient();
  const toast = useToast();
  const ev = d.event;
  const slug = ev.slug;
  const lock = locks(d).settings;
  const off = !!lock;
  const scoring = useQuery({ queryKey: ["judging-settings", slug], queryFn: () => api.judging.settings(slug) as Promise<Scoring> });
  const results = useQuery({ queryKey: ["results", slug], queryFn: () => api.voting.results(slug) as Promise<Results> });
  const voting = useQuery({ queryKey: ["voting", slug], queryFn: () => api.voting.settings(slug) as Promise<Voting> });
  const s = scoring.data;
  const v = voting.data;
  const published = Array.isArray(results.data) ? results.data : results.data?.results || null;
  const fullRanking = !Array.isArray(results.data) && !!results.data?.full_ranking;

  const [scoreMode, setScoreMode] = useState("raw");
  const [vis, setVis] = useState({ full: false, scores: false, ranks: false });
  const [win, setWin] = useState({ enabled: false, opens: "", closes: "" });
  const [elig, setElig] = useState("all_signed_in");
  const [busy, setBusy] = useState("");
  const [err, setErr] = useState<Record<string, string>>({});
  const [gallery, setGallery] = useState("");
  const [removal, setRemoval] = useState<{ count: number; part: "window" | "eligibility" } | null>(null);
  useEffect(() => {
    if (s) setScoreMode(s.score_mode || "raw");
  }, [s]);
  useEffect(() => {
    if (s) setVis({ full: fullRanking, scores: !!s.public_scores, ranks: !!s.public_ranks });
  }, [s, fullRanking]);
  useEffect(() => {
    if (!v) return;
    setWin({ enabled: !!v.enabled, opens: localInput(v.opens_at, ev.time_zone), closes: localInput(v.closes_at, ev.time_zone) });
    setElig(v.eligibility || "all_signed_in");
  }, [v, ev.time_zone]);
  const dirty = {
    ranking: !!s && scoreMode !== (s.score_mode || "raw"),
    vis: !!s && (vis.full !== fullRanking || vis.scores !== !!s.public_scores || vis.ranks !== !!s.public_ranks),
    window: !!v && (win.enabled !== !!v.enabled || win.opens !== localInput(v.opens_at, ev.time_zone) || win.closes !== localInput(v.closes_at, ev.time_zone)),
    eligibility: !!v && elig !== (v.eligibility || "all_signed_in"),
  };
  useUnsavedGuard(Object.values(dirty).some(Boolean));
  const ready = !scoring.isPending && !results.isPending && !voting.isPending;
  useAnchor(ready);
  if (!ready) return <TabLoading />;
  if (scoring.isError || voting.isError) return <Failed error={scoring.error || voting.error} retry={() => void Promise.all([scoring.refetch(), voting.refetch(), results.refetch()])} />;

  const run = async (part: string, fn: () => Promise<unknown>, done = "Saved") => {
    setBusy(part);
    setErr({ ...err, [part]: "" });
    try {
      await fn();
      toast(done);
    } catch (e) {
      setErr({ ...err, [part]: errorText(e) });
    } finally {
      setBusy("");
    }
  };
  const saveScoring = (body: Scoring) =>
    api.judging.setSettings(slug, body).then((r) => {
      qc.setQueryData(["judging-settings", slug], { ...s, ...(r as Scoring) });
    });
  const saveVis = () =>
    run("vis", async () => {
      await saveScoring({ public_scores: vis.scores, public_ranks: vis.ranks });
      if (published) {
        await api.voting.setResults(slug, { full_ranking: vis.full });
        await qc.invalidateQueries({ queryKey: ["results", slug] });
      }
    });
  // The server decides whether earlier votes would go (409 vote_removal_confirmation), so the count is never stale.
  const putVoting = async (part: "window" | "eligibility", confirm = false) => {
    setGallery("");
    const body = part === "window" ? { enabled: win.enabled, opens_at: win.opens, closes_at: win.closes, eligibility: v?.eligibility } : { enabled: v?.enabled, opens_at: v?.opens_at, closes_at: v?.closes_at, eligibility: elig };
    setBusy(part);
    setErr({ ...err, [part]: "" });
    try {
      const r = (await api.voting.setSettings(slug, { ...body, ...(confirm ? { confirm_vote_removal: true } : {}) })) as Voting & { votes_removed?: number };
      const { directory_listed: _ignored, ...rest } = r;
      void _ignored;
      qc.setQueryData(["voting", slug], { ...v, ...rest });
      toast(r.votes_removed ? `${r.votes_removed} earlier ${r.votes_removed === 1 ? "vote" : "votes"} removed` : "Saved");
    } catch (e) {
      if (e instanceof ApiError && e.code === "gallery_closed") setGallery(part);
      else if (e instanceof ApiError && e.code === "vote_removal_confirmation") setRemoval({ count: Number(e.body.votes_to_remove) || 0, part });
      else setErr({ ...err, [part]: errorText(e) });
    } finally {
      setBusy("");
    }
  };
  const galleryNote = (part: string) =>
    gallery === part && (
      <Notice tone="bad" title="Turn on Show entries on the event page first.">
        It is on <AppLink to={href("organiser", slug, "event-page") + "&tab=look"}>Event page → Look</AppLink>.
      </Notice>
    );
  const [label, tone] = STATUS[v?.status || "disabled"] || STATUS.disabled;
  const tz = ev.time_zone || "UTC";

  return (
    <>
      <LockNote text={lock} />
      <Panel id="ranking" title="How teams are ranked">
        <form onSubmit={submit(() => void run("ranking", () => saveScoring({ score_mode: scoreMode })))}>
          <Label htmlFor="res-score-mode">Score used for ranking</Label>
          <NativeSelect id="res-score-mode" value={scoreMode} disabled={off} onChange={(e) => setScoreMode(e.target.value)} className="w-full max-w-md">
            <option value="normalised">Average, adjusted for strict and generous judges</option>
            <option value="raw">Average score</option>
          </NativeSelect>
          <SaveRow busy={busy === "ranking"} disabled={off || !dirty.ranking} error={err.ranking} />
        </form>
      </Panel>
      <Panel title="Show on the public page" sub="Tip: Full ranking with scores off lists every team without a scoreboard.">
        <form onSubmit={submit(saveVis)}>
          <Label htmlFor="res-visibility">Visibility</Label>
          <NativeSelect id="res-visibility" value={String(vis.full)} disabled={off || !published} onChange={(e) => setVis({ ...vis, full: e.target.value === "true" })} className="w-full max-w-md">
            <option value="true">Full ranking</option>
            <option value="false">Winners only</option>
          </NativeSelect>
          <Check checked={vis.scores} disabled={off} onChange={(on) => setVis({ ...vis, scores: on })} hint="Teams always see their own score.">
            Show scores
          </Check>
          <Check checked={vis.ranks} disabled={off} onChange={(on) => setVis({ ...vis, ranks: on })}>
            Show ranks
          </Check>
          <SaveRow busy={busy === "vis"} disabled={off || !dirty.vis} error={err.vis} note={!published && !off ? "Publish results first to control what the public page shows." : undefined} />
        </form>
      </Panel>
      <Panel id="voting" title="People’s choice" action={<Badge tone={tone}>{label}</Badge>}>
        <form onSubmit={submit(() => void putVoting("window"))} id="voting-window">
          <Check checked={win.enabled} disabled={off} onChange={(on) => setWin({ ...win, enabled: on })}>
            Let visitors vote
          </Check>
          <div className="mt-2 grid gap-x-3 md:grid-cols-2">
            <div className="mb-3">
              <Label htmlFor="res-vote-opens">Starts · {tz}</Label>
              <Input id="res-vote-opens" type="datetime-local" value={win.opens} disabled={off} onChange={(e) => setWin({ ...win, opens: e.target.value })} />
            </div>
            <div className="mb-3">
              <Label htmlFor="res-vote-closes">Ends · {tz}</Label>
              <Input id="res-vote-closes" type="datetime-local" value={win.closes} disabled={off} onChange={(e) => setWin({ ...win, closes: e.target.value })} />
            </div>
          </div>
          {galleryNote("window")}
          <SaveRow label="Save voting window" busy={busy === "window"} disabled={off || !dirty.window} error={err.window} />
        </form>
        <form onSubmit={submit(() => void putVoting("eligibility"))} className="mt-5 border-t border-rule pt-4">
          <Label htmlFor="res-vote-eligibility">Who can vote</Label>
          <NativeSelect id="res-vote-eligibility" value={elig} disabled={off} onChange={(e) => setElig(e.target.value)} className="w-full max-w-md">
            <option value="all_signed_in">Anyone signed in</option>
            <option value="event_members">Approved event members</option>
            <option value="participants">Approved participants only</option>
          </NativeSelect>
          {galleryNote("eligibility")}
          <SaveRow busy={busy === "eligibility"} disabled={off || !dirty.eligibility} error={err.eligibility} />
        </form>
      </Panel>
      <AskDialog
        open={!!removal}
        onOpenChange={(o) => !o && setRemoval(null)}
        title="Remove earlier votes?"
        body={`This removes ${removal?.count} ${removal?.count === 1 ? "vote that no longer qualifies" : "votes that no longer qualify"}. This can’t be undone.`}
        confirmLabel="Save and remove votes"
        danger
        onConfirm={() => (removal ? putVoting(removal.part, true) : undefined)}
      />
    </>
  );
}
