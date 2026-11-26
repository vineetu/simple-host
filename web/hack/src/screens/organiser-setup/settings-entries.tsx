// Settings → Entries (the deadline and what teams hand in) and Settings →
// Reminders (how long before a deadline teams and judges are reminded).
import { useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { AskDialog } from "@kit/components/ui/dialog";
import { Button } from "@kit/components/ui/button";
import { RadioCards } from "@kit/components/ui/choice";
import { Field, FieldMessage, Input, Label, NativeSelect } from "@kit/components/ui/input";
import { Notice } from "@kit/components/ui/notice";
import { useToast } from "@kit/components/ui/toast";
import { useHack } from "@/lib/client";
import { dateTime } from "@/lib/time";
import { href } from "@/routes/resolve";
import { AppLink } from "@/app/nav";
import { Failed } from "@/app/states";
import type { TabProps } from "./Settings";
import { errorText, localInput, lockReason, LockNote, Panel, SaveFoot, useRefreshEvent, useUnsavedGuard } from "./common";
import {
  addReminder,
  impactLines,
  offsetLabel,
  PRESETS,
  presetLabel,
  sameOffsets,
  SHOW_OPTS,
  SHOW_ROWS,
  SUMMARY_OPTS,
  SUMMARY_ROWS,
  withField,
  type FieldMode,
  type Fields,
  type ReminderLimits,
} from "./settings";
import { SaveRow, submit, TabLoading, useAnchor } from "./settings-ui";

type Rules = { preset?: string; fields?: Fields; judge_first?: "video" | "site"; editable?: boolean; has_tracks?: boolean; summary?: string };

function Deadline({ d }: TabProps) {
  const api = useHack();
  const toast = useToast();
  const slug = d.event.slug;
  const ev = d.event;
  const refresh = useRefreshEvent(slug);
  const lock = lockReason(d);
  const saved = localInput(ev.submission_deadline, ev.time_zone);
  const [value, setValue] = useState(saved);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => setValue(saved), [saved]);
  useUnsavedGuard(value !== saved);
  const put = async (v: string) => {
    await api.event.update(slug, { submission_deadline: v });
    await refresh();
  };
  const save = async () => {
    if (!value) return setError("Choose a date and time.");
    setError("");
    setBusy(true);
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
    setError("");
    const before = saved;
    try {
      await put("");
      toast("Deadline cleared. Teams can keep changing sites and entries until you set a new one.", {
        undo: { label: "Undo", run: () => void put(before).catch((e) => toast(errorText(e), { bad: true })) },
      });
    } catch (e) {
      setError(errorText(e));
    }
  };
  if (ev.stage === "archived")
    return (
      <Panel id="deadline" title="Deadline">
        <p>{ev.submission_deadline ? "Entries closed " + dateTime(ev.submission_deadline, ev) + "." : "No deadline was set."}</p>
      </Panel>
    );
  return (
    <Panel id="deadline" title="Deadline">
      <form onSubmit={submit(save)} noValidate>
        <Field
          id="deadline-input"
          label={"Entries close · " + (ev.time_zone || "UTC")}
          error={error}
          hint={
            <>
              {ev.submission_deadline ? "Entries close " + dateTime(ev.submission_deadline, ev) + "." : "No deadline yet."} At the deadline, drafts with nothing missing are submitted for their teams. Drafts still missing something are judged too,
              marked Incomplete. You can exclude one on <AppLink to={href("organiser", slug, "entries")}>Entries</AppLink>.
            </>
          }
        >
          <Input id="deadline-input" type="datetime-local" value={value} disabled={!!lock} onChange={(e) => setValue(e.target.value)} className="max-w-72" aria-invalid={!!error} />
        </Field>
        {["closed", "judging", "results"].includes(ev.stage) && <p className="text-sm text-ink-soft">To reopen, move the event back to Building on Home.</p>}
        <SaveRow label="Save deadline" busy={busy} disabled={!!lock}>
          <Button disabled={!!lock || !ev.submission_deadline} onClick={() => void clear()}>
            Clear deadline
          </Button>
        </SaveRow>
      </form>
    </Panel>
  );
}

function HandIn({ d }: TabProps) {
  const api = useHack();
  const qc = useQueryClient();
  const toast = useToast();
  const slug = d.event.slug;
  const q = useQuery({ queryKey: ["entry-rules", slug], queryFn: () => api.entries.rules(slug) as Promise<Rules> });
  const [rules, setRules] = useState<Rules | null>(null);
  const [lastChosen, setLastChosen] = useState<string | null>(null);
  const [resetTo, setResetTo] = useState("");
  const [impact, setImpact] = useState<string[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  useEffect(() => {
    if (!q.data) return;
    setRules(q.data);
    if (q.data.preset && q.data.preset !== "custom") setLastChosen(q.data.preset);
  }, [q.data]);
  const dirty = !!rules && !!q.data && JSON.stringify(rules) !== JSON.stringify(q.data);
  useUnsavedGuard(dirty);
  if (q.isPending) return <TabLoading />;
  if (q.isError) return <Failed error={q.error} retry={() => q.refetch()} />;
  if (!rules) return null;
  const lock = lockReason(d) || (rules.editable === false ? "Entries are closed, so these can’t change. To reopen, move the event back to Building on Home." : "");
  const mode = (f: string) => (rules.fields?.[f] || "optional") as FieldMode;
  const tracks = ((d.event.tracks as unknown[]) || []).length;

  const pick = (p: string) => {
    setLastChosen(p);
    setRules({ ...rules, preset: p, fields: { ...PRESETS[p].fields }, judge_first: PRESETS[p].judge_first });
  };
  const body = () => {
    const b: Record<string, unknown> = { fields: rules.fields, judge_first: rules.judge_first };
    // Only a current preset is ever sent; a retired or custom one saves its lines alone.
    if (rules.preset && Object.hasOwn(PRESETS, rules.preset)) b.preset = rules.preset;
    return b;
  };
  const put = async () => {
    setBusy(true);
    try {
      const r = (await api.entries.setRules(slug, body())) as Rules;
      qc.setQueryData(["entry-rules", slug], r);
      void qc.invalidateQueries({ queryKey: ["entry-rules", slug] });
      toast("Saved");
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(false);
    }
  };
  const save = async () => {
    setError("");
    setBusy(true);
    let lines: string[] = [];
    try {
      const dr = (await api.entries.setRules(slug, { ...body(), dry_run: true })) as { impact?: { submitted_missing?: number; drafts_newly_blocked?: number } };
      lines = impactLines(dr.impact);
    } catch (e) {
      setBusy(false);
      return setError(errorText(e) || "Could not check the impact of this change");
    }
    setBusy(false);
    if (lines.length) setImpact(lines);
    else await put();
  };
  const row = (r: { id: string; label: string; help?: string }, opts: [FieldMode, string][]) => (
    <li key={r.id} className="flex flex-wrap items-center gap-x-4 gap-y-1 border-b border-rule py-2 last:border-b-0">
      <div className="min-w-0 flex-[1_1_14rem]">
        <Label htmlFor={"rule-" + r.id} className="mb-0 text-base text-ink">
          {r.label}
        </Label>
        {r.help && <p className="text-sm text-ink-soft">{r.help}</p>}
      </div>
      <NativeSelect id={"rule-" + r.id} value={mode(r.id)} disabled={!!lock} onChange={(e) => setRules({ ...rules, ...withField(rules, r.id, e.target.value as FieldMode) })} className="w-40">
        {opts.map(([v, t]) => (
          <option key={v} value={v}>
            {t}
          </option>
        ))}
      </NativeSelect>
    </li>
  );
  const summary = SUMMARY_ROWS.filter((r) => r.id !== "track" || rules.has_tracks).map((r) => (r.id === "track" ? { ...r, help: tracks + " tracks in this event" } : r));
  return (
    <Panel id="entry-rules" title="What teams hand in" sub="Start from the kind of event, then change any line. Teams can’t submit until everything Required is there.">
      {lock && <Notice title={lock} />}
      <form onSubmit={submit(save)} id="handin">
        <fieldset disabled={!!lock} className="m-0 min-w-0 border-0 p-0">
          <RadioCards
            label="What teams hand in"
            value={rules.preset && Object.hasOwn(PRESETS, rules.preset) ? rules.preset : ""}
            onChange={(p) => (dirty ? setResetTo(p) : pick(p))}
            options={Object.entries(PRESETS).map(([id, p]) => ({ value: id, title: p.label, hint: p.desc }))}
          />
        </fieldset>
        <h3 className="mt-5 type-label text-ink-soft">Summary card</h3>
        <ul className="m-0 list-none p-0">
          <li className="flex flex-wrap items-center gap-x-4 border-b border-rule py-2">
            <div className="min-w-0 flex-[1_1_14rem]">
              <p className="text-base">Project name</p>
              <p className="text-sm text-ink-soft">Always required</p>
            </div>
            <span className="w-40 px-3 text-ink-soft">Required</span>
          </li>
          {summary.map((r) => row(r, SUMMARY_OPTS))}
        </ul>
        <h3 className="mt-5 type-label text-ink-soft">Show your work</h3>
        <ul className="m-0 list-none p-0">{SHOW_ROWS.map((r) => row(r, SHOW_OPTS))}</ul>
        <p className="mt-2 text-sm text-ink-soft">One of: teams need at least one of the lines marked One of.{rules.summary ? " Here: " + rules.summary + "." : ""}</p>
        <h3 className="mt-5 type-label text-ink-soft">Judges see</h3>
        <div className="mt-1 flex flex-wrap items-center gap-3">
          <Label htmlFor="judge-first" className="mb-0 text-base text-ink">
            After the card, first
          </Label>
          <NativeSelect id="judge-first" value={rules.judge_first === "site" ? "site" : "video"} disabled={!!lock} onChange={(e) => setRules({ ...rules, judge_first: e.target.value as "video" | "site" })}>
            <option value="video">Video</option>
            <option value="site">Website</option>
          </NativeSelect>
        </div>
        <p className="mt-1 text-sm text-ink-soft">Every judge sees the summary card first. Video, website and code link events usually open the website first.</p>
        <SaveRow primary busy={busy} disabled={!!lock || !dirty} error={error} note={rules.preset === "custom" ? presetLabel(rules.preset, lastChosen) : undefined} />
      </form>
      <AskDialog
        open={!!resetTo}
        onOpenChange={(o) => !o && setResetTo("")}
        title={"Reset every line to " + (PRESETS[resetTo]?.label || "") + "?"}
        body="Your unsaved changes to this card are replaced."
        confirmLabel="Reset"
        onConfirm={() => pick(resetTo)}
      />
      <AskDialog open={!!impact} onOpenChange={(o) => !o && setImpact(null)} title="Save these changes to what teams hand in?" body={(impact || []).join(" ")} confirmLabel="Save" onConfirm={put} />
      {error && !dirty && <FieldMessage tone="bad">{error}</FieldMessage>}
    </Panel>
  );
}

export function EntriesTab({ d }: TabProps) {
  useAnchor(true);
  return (
    <>
      <LockNote text={lockReason(d)} />
      <Deadline d={d} />
      <HandIn d={d} />
    </>
  );
}

type Reminders = { offsets_minutes?: number[]; judging_ends_at?: string | null } & ReminderLimits;

export function RemindersTab({ d }: TabProps) {
  const api = useHack();
  const qc = useQueryClient();
  const toast = useToast();
  const slug = d.event.slug;
  const lock = lockReason(d);
  const q = useQuery({ queryKey: ["reminders", slug], queryFn: () => api.event.reminders(slug) as Promise<Reminders> });
  const saved = q.data?.offsets_minutes || [];
  const [draft, setDraft] = useState<number[]>([]);
  const [n, setN] = useState("");
  const [unit, setUnit] = useState<"days" | "hours" | "minutes">("days");
  const [addErr, setAddErr] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => setDraft(q.data?.offsets_minutes || []), [q.data]);
  const dirty = !sameOffsets(draft, saved);
  useUnsavedGuard(dirty);
  useAnchor(!q.isPending);
  if (q.isPending) return <TabLoading />;
  if (q.isError) return <Failed error={q.error} retry={() => q.refetch()} />;
  const lim = q.data;
  const max = lim.max_count ?? 5;
  const add = () => {
    const r = addReminder(draft, n, unit, lim);
    if (typeof r === "string") return setAddErr(r);
    setAddErr("");
    setN("");
    setDraft(r);
  };
  const save = async () => {
    setBusy(true);
    setError("");
    try {
      const r = (await api.event.setReminders(slug, { offsets_minutes: draft })) as Reminders;
      qc.setQueryData(["reminders", slug], r);
      toast("Saved");
    } catch (e) {
      setError(errorText(e) || "Could not save reminder times.");
    } finally {
      setBusy(false);
    }
  };
  return (
    <>
      <LockNote text={lock} />
      <Panel id="reminders" title="Reminder times" sub="Teams without a submitted entry are emailed this long before their deadline. Judges with teams left to score are reminded this long before judging closes.">
        <form onSubmit={submit(save)} noValidate>
          {draft.length ? (
            <ul aria-label="Reminder times" className="m-0 flex list-none flex-wrap gap-2 p-0">
              {draft.map((m) => (
                <li key={m} className="inline-flex items-center rounded-tag border border-ink-soft pl-3 text-sm">
                  {offsetLabel(m)} before
                  <button
                    type="button"
                    disabled={!!lock}
                    aria-label={`Remove the ${offsetLabel(m)} reminder`}
                    onClick={() => setDraft(draft.filter((x) => x !== m))}
                    className="size-11 cursor-pointer rounded-tag text-lg text-ink-soft hover:text-ink disabled:cursor-default"
                  >
                    ×
                  </button>
                </li>
              ))}
            </ul>
          ) : (
            <p className="text-ink-soft">No reminders set. Nobody is reminded before a deadline.</p>
          )}
          {!lock && (
            <>
              <div className="mt-4 flex flex-wrap items-end gap-2.5">
                <div>
                  <Label htmlFor="rem-n">Add a reminder</Label>
                  <Input id="rem-n" type="number" inputMode="numeric" min={1} value={n} onChange={(e) => setN(e.target.value)} className="w-28" aria-invalid={!!addErr} aria-describedby="rem-err" />
                </div>
                <div>
                  <Label htmlFor="rem-unit">Unit</Label>
                  <NativeSelect id="rem-unit" value={unit} onChange={(e) => setUnit(e.target.value as typeof unit)}>
                    <option value="days">days before</option>
                    <option value="hours">hours before</option>
                    <option value="minutes">minutes before</option>
                  </NativeSelect>
                </div>
                <Button disabled={draft.length >= max} onClick={add}>
                  Add
                </Button>
              </div>
              <FieldMessage tone="bad" id="rem-err">
                {addErr}
              </FieldMessage>
              <p className="mt-2 text-sm text-ink-soft">
                Up to {max}, each between {lim.min_minutes ?? 1} minutes and 14 days before the deadline.
              </p>
            </>
          )}
          <p className="mt-2 text-sm text-ink-soft">
            {lim.judging_ends_at ? (
              "Judging closes " + dateTime(lim.judging_ends_at, d.event) + "."
            ) : (
              <>
                Set when judging closes on <AppLink to={href("organiser", slug, "settings") + "&tab=judging#closes"}>Settings → Judging</AppLink> to remind judges.
              </>
            )}
          </p>
          {!lock && <SaveFoot dirty={dirty} busy={busy} error={error} onDiscard={() => setDraft(saved)} />}
        </form>
      </Panel>
    </>
  );
}
