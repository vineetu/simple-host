// Settings → Judging: the rubric, the tie-break criterion, when judging
// closes, and how judges are assigned (with organisers who also judge).
// Setup locks when judging starts (and stays locked after); going back to
// Entries closed unlocks it. When judging closes can change until the end.
import { useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError } from "@kit/api/call";
import { AskDialog } from "@kit/components/ui/dialog";
import { Button } from "@kit/components/ui/button";
import { Check, RadioCards } from "@kit/components/ui/choice";
import { CopyButton } from "@kit/components/ui/copy";
import { Field, FieldMessage, Input, Label, NativeSelect } from "@kit/components/ui/input";
import { Notice } from "@kit/components/ui/notice";
import { useToast } from "@kit/components/ui/toast";
import { useHack } from "@/lib/client";
import { dateTime } from "@/lib/time";
import { href } from "@/routes/resolve";
import { AppLink } from "@/app/nav";
import { Failed } from "@/app/states";
import type { TabProps } from "./Settings";
import { errorText, localInput, Panel, useRefreshEvent } from "./common";
import { weightTotal, type Criterion } from "./settings";
import { SaveRow, submit, TabLoading, useAnchor } from "./settings-ui";
import { useUnsaved } from "@kit/guard/unsaved";
import { LockNote, writeLock } from "@/shared/locks";

type JudgingSettings = { assignment_mode?: string; judges_per_team?: number; tie_criterion_id?: string | null; organiser_judges?: string[] | null };
type Person = { user_id: string; display_name?: string; email?: string; role?: string };

const MODES = [
  { value: "open", title: "Open", hint: "Judges pick any team from the full list." },
  { value: "automatic", title: "Automatic", hint: "Simple Hack assigns judges to teams evenly." },
  { value: "manual", title: "Manual", hint: "You assign each judge to specific teams." },
  { value: "panel", title: "Panel", hint: "Judges are grouped by track, one panel per track." },
];

const NONE: Criterion[] = [];
const codes = { judging_setup_locked: "Judging has started. Move the event back to Entries closed to change this." };

function Rubric({ slug, saved, off }: { slug: string; saved: Criterion[]; off: boolean }) {
  const api = useHack();
  const qc = useQueryClient();
  const toast = useToast();
  const [draft, setDraft] = useState<Criterion[]>(saved);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [deletes, setDeletes] = useState("");
  useEffect(() => setDraft(saved), [saved]);
  const dirty = JSON.stringify(draft) !== JSON.stringify(saved);
  useUnsaved(dirty);
  const total = weightTotal(draft);
  const set = (i: number, c: Partial<Criterion>) => setDraft(draft.map((x, j) => (j === i ? { ...x, ...c } : x)));
  const move = (i: number, dir: number) => {
    const next = [...draft];
    next.splice(i + dir, 0, next.splice(i, 1)[0]);
    setDraft(next);
  };
  const put = async (confirm: boolean) => {
    const body = {
      criteria: draft.map((c) => ({ id: c.id || "", name: c.name || "", description: c.description || "", weight: Number(c.weight) || 0, max_points: Number(c.max_points) || 1 })),
      ...(confirm ? { confirm_score_deletion: true } : {}),
    };
    const r = (await api.judging.setRubric(slug, body)) as { criteria?: Criterion[] };
    qc.setQueryData(["rubric", slug], r);
    toast("Rubric saved");
  };
  const save = async () => {
    if (draft.some((c) => !c.name.trim())) return setError("Give every criterion a name.");
    if (total !== 100) return setError("Weights must add up to 100%.");
    setError("");
    setBusy(true);
    try {
      await put(false);
    } catch (e) {
      if (e instanceof ApiError && e.code === "score_deletion_confirmation") setDeletes(e.message);
      else setError(errorText(e, codes));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Panel id="rubric" title="Rubric" sub="What judges score each team on. Weights add up to 100%.">
      <form onSubmit={submit(save)} noValidate>
        {draft.length === 0 && <p className="text-ink-soft">No criteria yet. Add one to start.</p>}
        <ol className="m-0 list-none p-0">
          {draft.map((c, i) => {
            const name = c.name || "this criterion";
            return (
              <li key={i} className="mb-3 rounded-field border border-rule px-3 pt-3 pb-1">
                <div className="grid gap-x-3 md:grid-cols-[1fr_7rem_7rem]">
                  <Field id={`crit-${i}-name`} label="Name">
                    <Input id={`crit-${i}-name`} value={c.name} disabled={off} maxLength={80} onChange={(e) => set(i, { name: e.target.value })} />
                  </Field>
                  <Field id={`crit-${i}-w`} label="Weight %">
                    <Input id={`crit-${i}-w`} type="number" min={0} max={100} value={String(c.weight ?? 0)} disabled={off} onChange={(e) => set(i, { weight: Number(e.target.value) || 0 })} />
                  </Field>
                  <Field id={`crit-${i}-p`} label="Max points">
                    <Input id={`crit-${i}-p`} type="number" min={1} max={10} value={String(c.max_points ?? 5)} disabled={off} onChange={(e) => set(i, { max_points: Number(e.target.value) || 1 })} />
                  </Field>
                </div>
                <Field id={`crit-${i}-d`} label="Description (optional)">
                  <Input id={`crit-${i}-d`} value={c.description || ""} disabled={off} maxLength={300} onChange={(e) => set(i, { description: e.target.value })} />
                </Field>
                <div className="mb-2 flex flex-wrap gap-2">
                  <Button disabled={off || i === 0} onClick={() => move(i, -1)} aria-label={`Move ${name} up`}>
                    Up
                  </Button>
                  <Button disabled={off || i === draft.length - 1} onClick={() => move(i, 1)} aria-label={`Move ${name} down`}>
                    Down
                  </Button>
                  <Button variant="danger" disabled={off || draft.length <= 1} onClick={() => setDraft(draft.filter((_, j) => j !== i))} aria-label={`Remove ${name}`}>
                    Remove
                  </Button>
                </div>
              </li>
            );
          })}
        </ol>
        <SaveRow primary label="Save rubric" busy={busy} disabled={off || !dirty} error={error} note={draft.length ? `Total weight: ${total}%${total === 100 ? "" : " (must add up to 100%)"}` : undefined}>
          <Button disabled={off || draft.length >= 10} onClick={() => setDraft([...draft, { id: "", name: "", description: "", weight: 0, max_points: 5 }])}>
            Add a criterion
          </Button>
        </SaveRow>
      </form>
      <AskDialog
        open={!!deletes}
        onOpenChange={(o) => !o && setDeletes("")}
        title="Delete scores?"
        body={deletes}
        confirmLabel="Delete scores and save"
        danger
        onConfirm={() => put(true).catch((e) => setError(errorText(e, codes)))}
      />
    </Panel>
  );
}

function Closes({ d }: TabProps) {
  const api = useHack();
  const toast = useToast();
  const ev = d.event;
  const slug = ev.slug;
  const refresh = useRefreshEvent(slug);
  const off = !!writeLock(d);
  const saved = localInput(ev.judging_ends_at, ev.time_zone);
  const [value, setValue] = useState(saved);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => setValue(saved), [saved]);
  useUnsaved(value !== saved);
  if (ev.stage === "archived")
    return (
      <Panel id="closes" title="Judging closes">
        <p>{ev.judging_ends_at ? "Judging closed " + dateTime(ev.judging_ends_at, ev) + "." : "No time was set."}</p>
      </Panel>
    );
  const put = async (v: string) => {
    await api.event.update(slug, { judging_ends_at: v });
    await refresh();
  };
  const save = async () => {
    setBusy(true);
    setError("");
    try {
      await put(value);
      toast("Saved");
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(false);
    }
  };
  const clear = async () => {
    const before = saved;
    try {
      await put("");
      toast("Cleared. Judges get no reminder before judging closes.", { undo: { label: "Undo", run: () => void put(before).catch((e) => toast(errorText(e), { bad: true })) } });
    } catch (e) {
      setError(errorText(e));
    }
  };
  return (
    <Panel id="closes" title="Judging closes">
      <form onSubmit={submit(save)}>
        <Field
          id="jg-closes"
          label={"Judging closes · " + (ev.time_zone || "UTC")}
          hint={
            <>
              Judges with teams left to score get a reminder before this time, at the times set in <AppLink to={href("organiser", slug, "settings") + "&tab=reminders"}>Reminders</AppLink>. Leave it empty for no judge reminder.
            </>
          }
        >
          <Input id="jg-closes" type="datetime-local" value={value} disabled={off} onChange={(e) => setValue(e.target.value)} className="max-w-72" />
        </Field>
        <SaveRow busy={busy} disabled={off || value === saved} error={error}>
          <Button disabled={off || !ev.judging_ends_at} onClick={() => void clear()}>
            Clear
          </Button>
        </SaveRow>
      </form>
    </Panel>
  );
}

export function JudgingTab({ d }: TabProps) {
  const api = useHack();
  const qc = useQueryClient();
  const toast = useToast();
  const ev = d.event;
  const slug = ev.slug;
  const lock = writeLock(d);
  const setupLocked = !!ev.judging_locked_at || ["judging", "results", "archived"].includes(ev.stage);
  const off = setupLocked || !!lock;
  const rubric = useQuery({ queryKey: ["rubric", slug], queryFn: () => api.judging.rubric(slug) as Promise<{ criteria?: Criterion[] }> });
  const settings = useQuery({ queryKey: ["judging-settings", slug], queryFn: () => api.judging.settings(slug) as Promise<JudgingSettings> });
  const people = useQuery({ queryKey: ["people", slug], queryFn: async () => ((await api.people.list(slug)) as unknown as Person[]) || [] });
  const s = settings.data;
  const [tie, setTie] = useState("");
  const [mode, setMode] = useState("open");
  const [perTeam, setPerTeam] = useState("2");
  const [orgJudges, setOrgJudges] = useState<string[]>([]);
  const [busy, setBusy] = useState<"" | "tie" | "mode">("");
  const [tieErr, setTieErr] = useState("");
  const [modeErr, setModeErr] = useState("");
  useEffect(() => {
    if (!s) return;
    setTie(s.tie_criterion_id || "");
    setMode(s.assignment_mode || "open");
    setPerTeam(String(s.judges_per_team || 2));
    setOrgJudges(s.organiser_judges || []);
  }, [s]);
  const modeDirty = !!s && (mode !== (s.assignment_mode || "open") || Number(perTeam) !== (s.judges_per_team || 2) || JSON.stringify([...orgJudges].sort()) !== JSON.stringify([...(s.organiser_judges || [])].sort()));
  useUnsaved(modeDirty || (!!s && tie !== (s.tie_criterion_id || "")));
  const ready = !rubric.isPending && !settings.isPending && !people.isPending;
  useAnchor(ready);
  if (!ready) return <TabLoading />;
  if (rubric.isError || settings.isError) return <Failed error={rubric.error || settings.error} retry={() => void Promise.all([rubric.refetch(), settings.refetch()])} />;
  const criteria = rubric.data?.criteria || NONE;
  const organisers = (people.data || []).filter((p) => p.role === "organiser");
  const patch = async (which: "tie" | "mode", body: Record<string, unknown>, setErr: (e: string) => void) => {
    setBusy(which);
    setErr("");
    try {
      const r = (await api.judging.setSettings(slug, body)) as JudgingSettings;
      qc.setQueryData(["judging-settings", slug], { ...s, ...r });
      toast("Saved");
    } catch (e) {
      setErr(errorText(e, codes));
    } finally {
      setBusy("");
    }
  };
  const saveMode = () => {
    const n = Number(perTeam);
    if (mode === "automatic" && (!Number.isInteger(n) || n < 1 || n > 20)) return setModeErr("Judges per team must be between 1 and 20.");
    void patch("mode", { assignment_mode: mode, judges_per_team: mode === "automatic" ? n : s?.judges_per_team || 2, ...(people.isSuccess ? { organiser_judges: orgJudges } : {}) }, setModeErr);
  };
  const judgeLink = (d.organiser as { judge_url?: string } | undefined)?.judge_url || "";
  return (
    <>
      <LockNote reason={lock} />
      {ev.stage === "judging" && !lock && <Notice title="Locked while judging.">Move the event back to Entries closed to change the rubric, tie-break and how judges are assigned.</Notice>}
      <Rubric slug={slug} saved={criteria} off={off} />
      <Panel id="tie-break" title="Tie-break" sub={off ? undefined : "You can change this until judging starts."}>
        <form onSubmit={submit(() => void patch("tie", { tie_criterion_id: tie }, setTieErr))}>
          <Label htmlFor="jg-tie">First tie criterion</Label>
          <NativeSelect id="jg-tie" value={tie} disabled={off} onChange={(e) => setTie(e.target.value)} className="w-full max-w-sm">
            <option value="">Pick a criterion</option>
            {criteria.map((c) => (
              <option key={c.id} value={c.id}>
                {c.name}
              </option>
            ))}
          </NativeSelect>
          <SaveRow busy={busy === "tie"} disabled={off || tie === (s?.tie_criterion_id || "")} error={tieErr} />
        </form>
      </Panel>
      <Closes d={d} />
      <Panel id="mode" title="How judges are assigned">
        <form onSubmit={submit(saveMode)} noValidate>
          <fieldset disabled={off} className="m-0 min-w-0 border-0 p-0">
            <RadioCards label="How judges are assigned" value={mode} onChange={setMode} options={MODES} />
          </fieldset>
          {mode === "automatic" && (
            <div className="mt-4">
              <Field id="jg-per" label="Judges per team">
                <Input id="jg-per" type="number" min={1} max={20} value={perTeam} disabled={off} onChange={(e) => setPerTeam(e.target.value)} className="w-28" />
              </Field>
            </div>
          )}
          {!["closed", "judging", "results", "archived"].includes(ev.stage) && <Notice title="You can set this before entries close, but teams may still be forming." />}
          <section id="organiser-judges" aria-label="Organisers who also judge" className="mt-4">
            <h3 className="type-label text-ink-soft">Organisers who also judge</h3>
            {people.isError ? (
              <FieldMessage tone="bad">The organisers could not be loaded, so this part is left as it is.</FieldMessage>
            ) : (
              organisers.map((p) => (
                <Check key={p.user_id} checked={orgJudges.includes(p.user_id)} disabled={off} onChange={(on) => setOrgJudges(on ? [...orgJudges, p.user_id] : orgJudges.filter((x) => x !== p.user_id))}>
                  {p.display_name || p.email}
                </Check>
              ))
            )}
            {judgeLink && <CopyButton text={judgeLink} label="Copy judge link" className="mt-2" />}
          </section>
          <SaveRow busy={busy === "mode"} disabled={off || !modeDirty} error={modeErr} />
        </form>
      </Panel>
    </>
  );
}
