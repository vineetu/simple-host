// Entry (/e/{slug}/team?s=entry): the team's entry. A status block (Draft /
// Submitted, the deadline, what is still missing, Submit), then the form in
// three cards (Summary card, Show your work, More detail) beside a live "How
// judges see it" card and the rubric. Saving a draft checks nothing; Submit
// saves first, then the server checks the event's rules and names what is
// missing. A teammate's save in between is a real choice (Keep mine / Take
// theirs). After the team's deadline the entry is shown read-only.
import { useEffect, useRef, useState, type ReactNode } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Badge } from "@kit/components/ui/badge";
import { Button } from "@kit/components/ui/button";
import { RadioCards } from "@kit/components/ui/choice";
import { AskDialog, Dialog, DialogActions } from "@kit/components/ui/dialog";
import { EmptyState } from "@kit/components/ui/empty-state";
import { FieldMessage, Input, Label, NativeSelect, Textarea } from "@kit/components/ui/input";
import { Notice } from "@kit/components/ui/notice";
import { PageTitle } from "@kit/components/ui/page-shell";
import { Skeleton } from "@kit/components/ui/skeleton";
import { useToast } from "@kit/components/ui/toast";
import { ApiError } from "@kit/api/call";
import { cn } from "@kit/lib/utils";
import { paths, useHack } from "@/lib/client";
import { AI_TOOLS, MATURITY, safeHref } from "@/lib/labels";
import { dateTime } from "@/lib/time";
import { href } from "@/routes/resolve";
import { useEventDetail } from "@/app/data";
import { AppLink } from "@/app/nav";
import { Failed } from "@/app/states";
import type { ScreenProps } from "@/routes/registry";
import { EntryCard } from "@/shared/EntryCard";
import { Screenshot } from "@/shared/Screenshot";
import { evOf, teamOf } from "../team/data";
import { blocked, codeOf, say, signedOut, ucfirst } from "../team/words";
import { putBytes } from "../website/upload";
import {
  CLOSED,
  HELP,
  LABEL,
  MAXLEN,
  SHOW,
  SUMMARY,
  addTool,
  conflicts,
  diff,
  fieldOf,
  hasAnyContent,
  marks as marksFor,
  mode,
  need,
  tightened,
  valuesOf,
  type Criterion,
  type Entry as EntryT,
  type EntryData,
  type Values,
} from "./data";
import { useUnsaved } from "@kit/guard/unsaved";

const TOOL_SUGGESTIONS = ["Canva", "Google Sheets", "Figma", "Supabase", "Python", "Airtable"];
const AI = AI_TOOLS.map((t) => t.toLowerCase());
const IMAGE_TYPES = ["image/png", "image/jpeg", "image/webp"];

const Title = () => <PageTitle>Entry</PageTitle>;

export default function Entry({ match }: ScreenProps) {
  const slug = match.slug!;
  const api = useHack();
  const q = useQuery({ queryKey: ["entry", slug], queryFn: () => api.entries.get(slug) as Promise<EntryData> });
  // The rubric is a side card: when it does not load, it is left out.
  const rubric = useQuery({
    queryKey: ["rubric", slug],
    queryFn: () => api.judging.rubric(slug).then((r) => ((r as { criteria?: Criterion[] }).criteria || []) as Criterion[], () => [] as Criterion[]),
  });
  // A refused submit's note and marks outlive the form being read again.
  const [refused, setRefused] = useState<{ note: string; marks: Record<string, string> } | null>(null);

  if (q.isPending)
    return (
      <>
        <Title />
        <div className="mt-6" role="status" aria-busy="true" aria-label="Loading your entry">
          <Skeleton className="h-24 w-full" />
          <div className="mt-5 grid gap-5 md:grid-cols-[minmax(0,1fr)_20rem]">
            <Skeleton className="h-96" />
            <Skeleton className="h-64" />
          </div>
        </div>
      </>
    );
  if (q.isError) {
    if (codeOf(q.error) === "no_team")
      return (
        <>
          <Title />
          <EmptyState
            title="Join or start a team first"
            action={
              <Button asChild>
                <AppLink to={href("participant", slug, "team")}>Go to Team</AppLink>
              </Button>
            }
          />
        </>
      );
    return (
      <>
        <Title />
        <Failed error={q.error} retry={() => q.refetch()} />
      </>
    );
  }
  const data = q.data;
  const criteria = rubric.data || [];
  if (data.editable === false) return <ReadOnly slug={slug} data={data} criteria={criteria} />;
  const e = data.entry || {};
  return (
    <Editor
      key={(e.updated_at || "") + "|" + (data.status || "") + "|" + (e.has_screenshot ? 1 : 0)}
      slug={slug}
      data={data}
      criteria={criteria}
      refused={refused}
      setRefused={setRefused}
    />
  );
}

// ------------------------------------------------------------- editing ---

function Editor({
  slug,
  data,
  criteria,
  refused,
  setRefused,
}: {
  slug: string;
  data: EntryData;
  criteria: Criterion[];
  refused: { note: string; marks: Record<string, string> } | null;
  setRefused: (r: { note: string; marks: Record<string, string> } | null) => void;
}) {
  const api = useHack();
  const qc = useQueryClient();
  const toast = useToast();
  const d = useEventDetail(slug).data!;
  const ev = evOf(d);
  const team = teamOf(d);
  const entry = data.entry || {};
  const rules = data.rules || { fields: {} };
  const tracks = ev.tracks || [];
  const hasTracks = typeof rules.has_tracks === "boolean" ? rules.has_tracks : tracks.length > 0;
  const m = (f: string) => mode(rules, hasTracks, f);
  const status = data.status || "draft";
  const submitted = status === "submitted";
  const missing = data.missing || [];
  const deadline = team?.deadline || ev.submission_deadline;
  const deadlineText = deadline ? dateTime(deadline, ev) : "";

  const [v, setV] = useState<Values>(() => valuesOf(entry));
  const [toolText, setToolText] = useState("");
  const [errs, setErrs] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState("");
  const [ask, setAsk] = useState<"" | "unsubmit" | "shot">("");
  const [conflict, setConflict] = useState<{ mine: Record<string, unknown>; newer: EntryT } | null>(null);
  const shotInput = useRef<HTMLInputElement>(null);
  const shown = { ...errs, ...(refused?.marks || {}) };
  const set = <K extends keyof Values>(k: K, val: Values[K]) => setV((o) => ({ ...o, [k]: val }));
  const body = diff(entry, v, { track: m("track") !== "off" });
  const dirty = Object.keys(body).length > 0;

  // Leaving with unsaved lines asks first.
  useUnsaved(dirty);
  // A refused submit moves focus to the first line it names.
  useEffect(() => {
    if (!refused) return;
    const first = Object.keys(refused.marks)[0];
    const target = first === "one_of" ? "en-video_url" : first === "maturity" ? "en-maturity" : first === "tools" ? "en-tools-input" : "en-" + first;
    document.getElementById(target)?.focus();
  }, [refused]);

  const put = (next: EntryData) => qc.setQueryData(["entry", slug], next);
  const reread = () => qc.invalidateQueries({ queryKey: ["entry", slug] });

  /** save writes the changed lines; it answers the saved entry, or null when it did not save. */
  const save = async (quiet = false): Promise<EntryData | null> => {
    setErrs({});
    if (!dirty) {
      if (!quiet) toast("Saved");
      return data;
    }
    const sent = { ...body, updated_at: entry.updated_at || "" };
    try {
      const r = (await api.entries.save(slug, sent)) as EntryData;
      return r;
    } catch (err) {
      if (signedOut(err)) return null;
      const code = codeOf(err);
      if (code === "entry_changed") {
        setConflict({ mine: body, newer: ((err as ApiError).body.entry || {}) as EntryT });
        return null;
      }
      if (blocked(err)) {
        toast(say(err), { bad: true });
        await reread();
        return null;
      }
      if (code === "entry_would_be_incomplete") {
        toast(say(err), { bad: true });
        setRefused({ note: "", marks: marksFor(((err as ApiError).body.missing as string[]) || [], rules) });
        return null;
      }
      const fields = fieldOf(code, sent);
      if (fields.length) setErrs(Object.fromEntries(fields.map((f) => [f, say(err)])));
      else toast(say(err), { bad: true });
      return null;
    }
  };

  const onSave = async () => {
    setBusy("save");
    const r = await save();
    setBusy("");
    if (r && r !== data) {
      setRefused(null);
      toast("Saved");
      put(r);
    }
  };

  const onSubmit = async () => {
    setBusy("submit");
    const saved = await save(true);
    if (!saved) return setBusy("");
    if (saved !== data) put(saved);
    try {
      const r = (await api.entries.submit(slug)) as EntryData;
      setRefused(null);
      put(r);
    } catch (err) {
      const code = codeOf(err);
      if (signedOut(err)) {
        /* went to sign-in */
      } else if (code === "entry_incomplete" || code === "entry_would_be_incomplete") {
        setRefused({ note: say(err, "Add these before you submit."), marks: marksFor(((err as ApiError).body.missing as string[]) || [], rules) });
      } else if (code === "submissions_closed") {
        toast(say(err), { bad: true });
        await reread();
      } else toast(say(err, "Could not submit."), { bad: true });
    } finally {
      setBusy("");
    }
  };

  const onShot = async (file: File | undefined) => {
    if (!file) return;
    setErrs({});
    if (file.size > 2 * 1024 * 1024 || (file.type && !IMAGE_TYPES.includes(file.type))) {
      setErrs({ screenshot: "Use a PNG, JPEG or WebP image, up to 2 MB." });
      if (shotInput.current) shotInput.current.value = "";
      return;
    }
    setBusy("shot");
    try {
      const r = await putBytes<EntryData>("PUT", paths.entryScreenshot(slug), file, file.type);
      toast("Screenshot saved");
      put(r);
    } catch (err) {
      if (signedOut(err)) return;
      if (blocked(err)) {
        toast(say(err), { bad: true });
        await reread();
      } else if (fieldOf(codeOf(err), {}).includes("screenshot")) setErrs({ screenshot: say(err) });
      else toast(say(err), { bad: true });
    } finally {
      setBusy("");
      if (shotInput.current) shotInput.current.value = "";
    }
  };

  const trackName = tracks.find((t) => t.slug === v.track)?.name || "";
  const preview = {
    title: v.title || entry.title || "Untitled",
    team_name: team?.name || "",
    team_members: (team?.members || [])
      .map((x) => x.display_name)
      .filter(Boolean)
      .join(", "),
    track: m("track") === "off" ? entry.track : trackName ? { name: trackName } : null,
    problem: v.problem,
    tagline: v.tagline,
    audience: v.audience,
    tools: v.tools,
    maturity: v.maturity as EntryT["maturity"],
  };

  // --- status block ---
  const by = data.submit_auto ? "" : data.submitted_by ? " by " + data.submitted_by : "";
  const statusBlock = submitted ? (
    <StatusBox
      tone="ok"
      title={data.complete ? (data.submit_auto ? "Submitted automatically" : "Submitted" + (data.submitted_at ? " · " + dateTime(data.submitted_at, ev) : "") + by) : "Submitted"}
      action={<Button onClick={() => setAsk("unsubmit")}>Move back to draft</Button>}
    >
      {data.complete ? "Judges will see this. You can still change it until " + (deadlineText || "the deadline") + ". It stays submitted." : tightened(missing)}
    </StatusBox>
  ) : (
    <StatusBox
      title={data.complete ? "Draft · ready to submit" : "Draft · not submitted yet"}
      action={
        <Button variant="primary" disabled={!!busy} onClick={onSubmit}>
          Submit entry
        </Button>
      }
      need={need(data)}
    >
      {"Submit by " + (deadlineText || "the deadline") + ". "}
      {data.complete ? "If you don’t, it is submitted for you at the deadline." : "Not complete yet. If it is still not complete at the deadline, it is judged as it is, marked incomplete."}
    </StatusBox>
  );

  const text = (f: "title" | "problem" | "tagline" | "audience" | "video_url" | "code_url", type?: string) =>
    m(f) === "off" ? null : (
      <Line
        id={"en-" + f}
        label={LABEL[f]}
        optional={m(f) === "optional" && SUMMARY.includes(f)}
        count={MAXLEN[f] ? v[f].length + " / " + MAXLEN[f] : undefined}
        hint={HELP[f]}
        error={shown[f]}
      >
        <Input
          id={"en-" + f}
          type={type}
          maxLength={MAXLEN[f]}
          value={v[f]}
          aria-invalid={!!shown[f] || undefined}
          aria-describedby={"en-" + f + "-msg"}
          onChange={(e) => set(f, e.target.value)}
        />
      </Line>
    );

  const site = data.site || {};
  const published = !!(site.exists && site.live_version);
  const oneOfCount = SHOW.filter((f) => m(f) === "one_of").length;
  const evidenceUnmet = missing.some((f) => ["video_url", "site", "code_url", "screenshot", "one_of"].includes(f));
  const descMode = m("description");

  return (
    <>
      <Title />
      {refused?.note && <Notice tone="bad" title={refused.note} />}
      {statusBlock}
      <div className="mt-5 grid items-start gap-5 md:grid-cols-[minmax(0,1fr)_20rem]">
        <div className="min-w-0">
          {m("title") !== "off" && (
            <Card title="Summary card" sub="Judges read this first, in about 20 seconds. Keep each line short.">
              {text("title")}
              {text("problem")}
              {text("tagline")}
              {text("audience")}
              {m("track") !== "off" && (
                <Line id="en-track" label={LABEL.track} hint="Your team’s track. Changing it here changes it for the whole team." error={shown.track}>
                  <NativeSelect id="en-track" className="w-full" value={v.track} aria-describedby="en-track-msg" onChange={(e) => set("track", e.target.value)}>
                    <option value="">No track</option>
                    {tracks.map((t) => (
                      <option key={t.slug} value={t.slug}>
                        {t.name}
                      </option>
                    ))}
                  </NativeSelect>
                </Line>
              )}
              {m("tools") !== "off" && (
                <Tools tools={v.tools} text={toolText} setText={setToolText} onChange={(t) => set("tools", t)} error={shown.tools} />
              )}
              {m("maturity") !== "off" && (
                <fieldset className="mb-4 min-w-0" id="en-maturity" tabIndex={-1}>
                  <legend className="mb-1.5 text-sm font-medium text-ink-soft">{LABEL.maturity}</legend>
                  <RadioCards
                    label={LABEL.maturity}
                    value={v.maturity}
                    onChange={(x) => set("maturity", x)}
                    options={(["working", "partial", "concept"] as const).map((k) => ({ value: k, title: MATURITY[k].label, hint: ucfirst(MATURITY[k].note) }))}
                  />
                  {shown.maturity && <FieldMessage tone="bad">{shown.maturity}</FieldMessage>}
                </fieldset>
              )}
            </Card>
          )}
          {!SHOW.every((f) => m(f) === "off") && (
            <Card
              title="Show your work"
              className={cn(evidenceUnmet && "border-bad")}
              sub={
                rules.summary ? (
                  <>
                    This event asks for {rules.summary}.{oneOfCount > 1 && m("code_url") !== "required" ? " One is enough." : ""}
                  </>
                ) : undefined
              }
            >
              {shown.one_of && <FieldMessage tone="bad">{shown.one_of}</FieldMessage>}
              <div className="mt-3">
                {text("video_url", "url")}
                {m("site") !== "off" && (
                  <div className="mb-4">
                    <p className="mb-1.5 text-sm font-medium text-ink-soft">{LABEL.site}</p>
                    <div className="flex flex-wrap items-center gap-3">
                      {published ? <Badge tone="ok">Published</Badge> : <Badge>Not published yet</Badge>}
                      {!published && (
                        <Button asChild>
                          <AppLink to={href("participant", slug, "website")}>Publish your website</AppLink>
                        </Button>
                      )}
                    </div>
                  </div>
                )}
                {text("code_url", "url")}
                {m("screenshot") !== "off" && (
                  <Line id="en-shot" label={LABEL.screenshot} optional={m("screenshot") === "optional"} error={shown.screenshot} hint="Screenshot changes save straight away.">
                    {entry.has_screenshot && <Screenshot path={paths.entryScreenshot(slug)} alt="Screenshot" />}
                    <input
                      ref={shotInput}
                      id="en-shot"
                      type="file"
                      accept="image/png,image/jpeg,image/webp"
                      aria-describedby="en-shot-msg"
                      disabled={busy === "shot"}
                      onChange={(e) => onShot(e.target.files?.[0])}
                      className="mt-3 block min-h-11 w-full max-w-full text-sm file:mr-3 file:min-h-11 file:cursor-pointer file:rounded-wobble file:border-[1.5px] file:border-ink-soft file:bg-card file:px-4 file:text-base file:text-ink"
                    />
                    {entry.has_screenshot && (
                      <Button className="mt-3" onClick={() => setAsk("shot")}>
                        Remove screenshot
                      </Button>
                    )}
                  </Line>
                )}
              </div>
            </Card>
          )}
          {descMode !== "off" && (
            <details className="mt-5 rounded-wobble border border-rule bg-card px-4 py-1 md:px-5" open={descMode === "required" || !!entry.description || undefined}>
              <summary className="flex min-h-11 cursor-pointer items-center type-section">More detail {descMode === "required" ? "(required)" : "(optional)"}</summary>
              <div className="pt-2 pb-3">
                <Line id="en-description" label={LABEL.description} error={shown.description}>
                  <Textarea
                    id="en-description"
                    maxLength={5000}
                    value={v.description}
                    aria-invalid={!!shown.description || undefined}
                    aria-describedby="en-description-msg"
                    onChange={(e) => set("description", e.target.value)}
                  />
                </Line>
              </div>
            </details>
          )}
          <p className="mt-4 text-sm text-ink-soft">After results, your summary card, video and website can appear on the event’s public page if the organisers show your entry.</p>
          <div className="sticky bottom-[var(--sh-bottom-bar,0px)] z-10 -mx-4 mt-4 flex flex-wrap items-center gap-2 border-t border-rule bg-paper px-4 py-3 md:mx-0 md:px-0">
            {submitted ? (
              <Button variant="primary" disabled={!!busy} onClick={onSave}>
                Save changes
              </Button>
            ) : (
              <>
                <Button disabled={!!busy} onClick={onSave}>
                  Save draft
                </Button>
                <Button disabled={!!busy} onClick={onSubmit}>
                  Submit entry
                </Button>
              </>
            )}
            <span className="hidden text-sm text-ink-soft md:inline">{submitted ? "Each change is checked against what the event asks for." : "Saving a draft never checks anything."}</span>
          </div>
        </div>
        <aside className="min-w-0">
          <p className="mb-2 text-sm text-ink-soft">How judges see it</p>
          <EntryCard entry={preview} compact />
          <Rubric criteria={criteria} />
        </aside>
      </div>
      <AskDialog
        open={ask === "unsubmit"}
        onOpenChange={(o) => !o && setAsk("")}
        title={"Move " + (entry.title || "this entry") + " back to draft?"}
        body="It stops counting as submitted. At the deadline a complete draft is submitted for you, and an incomplete one is still judged, marked incomplete."
        confirmLabel="Move to draft"
        onConfirm={async () => {
          try {
            put((await api.entries.unsubmit(slug)) as EntryData);
          } catch (err) {
            if (!signedOut(err)) toast(say(err, "Could not move this entry back to draft."), { bad: true });
          }
        }}
      />
      <AskDialog
        open={ask === "shot"}
        onOpenChange={(o) => !o && setAsk("")}
        title="Remove the screenshot?"
        body="It comes off your entry."
        confirmLabel="Remove"
        danger
        onConfirm={async () => {
          try {
            const r = (await api.entries.removeScreenshot(slug)) as EntryData;
            toast("Screenshot removed");
            put(r);
          } catch (err) {
            if (signedOut(err)) return;
            toast(say(err), { bad: true });
            if (blocked(err)) await reread();
          }
        }}
      />
      <Conflict
        slug={slug}
        open={!!conflict}
        entry={entry}
        c={conflict}
        onClose={() => setConflict(null)}
        onSaved={(r) => {
          toast("Saved");
          put(r);
        }}
      />
    </>
  );
}

function StatusBox({ tone, title, children, action, need }: { tone?: "ok"; title: string; children: ReactNode; action?: ReactNode; need?: { text: string; ok: boolean }[] }) {
  return (
    <section
      aria-label="Entry status"
      className={cn("mt-5 flex flex-wrap items-start gap-x-4 gap-y-3 rounded-wobble border-[1.5px] px-4 py-4 md:px-5", tone === "ok" ? "border-good bg-card" : "border-ink-soft bg-card")}
    >
      <div className="min-w-0 flex-[1_1_18rem]">
        <p className={cn("type-section", tone === "ok" && "text-good")}>{title}</p>
        <p className="mt-0.5 text-sm text-ink-soft">{children}</p>
        {need && (
          <ul className="m-0 mt-2 list-none p-0" aria-label="What’s needed">
            {need.map((n) => (
              <li key={n.text} className="flex items-start gap-2 py-0.5 text-sm">
                <span aria-hidden="true" className={cn("mt-1 grid size-3.5 flex-none place-items-center rounded-tag border-[1.5px]", n.ok ? "border-good bg-good" : "border-ink-soft")} />
                <span>
                  <span className="sr-only">{n.ok ? "Done: " : "To do: "}</span>
                  {n.text}
                </span>
              </li>
            ))}
          </ul>
        )}
      </div>
      {action && <div className="flex flex-none">{action}</div>}
    </section>
  );
}

function Card({ title, sub, children, className }: { title: string; sub?: ReactNode; children: ReactNode; className?: string }) {
  return (
    <section className={cn("mt-5 rounded-wobble border border-rule bg-card px-4 py-4 first:mt-0 md:px-5", className)}>
      <h2 className="type-section">{title}</h2>
      {sub && <p className="mt-0.5 mb-3 text-sm text-ink-soft">{sub}</p>}
      {children}
    </section>
  );
}

function Line({ id, label, optional, count, hint, error, children }: { id: string; label: string; optional?: boolean; count?: string; hint?: string; error?: string; children: ReactNode }) {
  return (
    <div className="mb-4">
      <div className="flex flex-wrap items-baseline gap-x-2">
        <Label htmlFor={id}>{label}</Label>
        {optional && <span className="text-sm text-ink-faint">(optional)</span>}
        {count && <span className="ml-auto text-sm text-ink-faint">{count}</span>}
      </div>
      {children}
      {error ? (
        <FieldMessage tone="bad" id={id + "-msg"}>
          {error}
        </FieldMessage>
      ) : hint ? (
        <p id={id + "-msg"} className="mt-1.5 text-sm text-ink-soft">
          {hint}
        </p>
      ) : null}
    </div>
  );
}

function Tools({ tools, text, setText, onChange, error }: { tools: string[]; text: string; setText: (t: string) => void; onChange: (t: string[]) => void; error?: string }) {
  const add = (s: string) => onChange(addTool(tools, s));
  const sugg = (list: string[]) => list.filter((s) => !tools.some((t) => t.toLowerCase() === s.toLowerCase()));
  return (
    <div className="mb-4" role="group" aria-labelledby="en-tools-l">
      <p id="en-tools-l" className="mb-1.5 text-sm font-medium text-ink-soft">
        {LABEL.tools}
      </p>
      <div className="flex flex-wrap items-center gap-2 rounded-field border-[1.5px] border-ink-soft bg-paper p-1.5">
        {tools.length > 0 && (
          <ul className="m-0 flex list-none flex-wrap gap-1.5 p-0" aria-label="Tools added">
            {tools.map((t, i) => (
              <li key={t} className={cn("inline-flex min-h-11 items-center rounded-tag border pl-3", AI.includes(t.toLowerCase()) ? "border-accent text-accent" : "border-ink-soft text-ink")}>
                {t}
                <button type="button" aria-label={"Remove " + t} className="size-11 cursor-pointer rounded-tag text-lg text-ink-soft hover:text-ink" onClick={() => onChange(tools.filter((_, j) => j !== i))}>
                  ×
                </button>
              </li>
            ))}
          </ul>
        )}
        <input
          id="en-tools-input"
          aria-label="Add a tool"
          placeholder="Type and press Enter"
          disabled={tools.length >= 10}
          value={text}
          aria-describedby="en-tools-msg"
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" || e.key === ",") {
              e.preventDefault();
              add(text);
              setText("");
            } else if (e.key === "Backspace" && !text && tools.length) onChange(tools.slice(0, -1));
          }}
          className="min-h-11 min-w-40 flex-1 bg-transparent px-2 text-base text-ink placeholder:text-ink-faint focus-visible:outline-none"
        />
      </div>
      {(
        [
          ["AI", AI_TOOLS],
          ["Tools", TOOL_SUGGESTIONS],
        ] as const
      ).map(([k, list]) =>
        sugg([...list]).length ? (
          <div key={k} className="mt-2 flex flex-wrap items-center gap-1.5">
            <span className="w-12 text-sm text-ink-faint">{k}</span>
            {sugg([...list]).map((s) => (
              <button key={s} type="button" disabled={tools.length >= 10} onClick={() => add(s)} className="min-h-11 cursor-pointer rounded-tag border border-rule bg-card px-3 text-sm text-ink-soft hover:text-ink disabled:opacity-55">
                + {s}
              </button>
            ))}
          </div>
        ) : null,
      )}
      {error ? (
        <FieldMessage tone="bad" id="en-tools-msg">
          {error}
        </FieldMessage>
      ) : (
        <p id="en-tools-msg" className="mt-1.5 text-sm text-ink-soft">
          Up to 10. Pick from the suggestions or type your own.
        </p>
      )}
    </div>
  );
}

function Rubric({ criteria }: { criteria: Criterion[] }) {
  if (!criteria.length) return null;
  const total = criteria.reduce((s, c) => s + (Number(c.max_points) || 0), 0);
  return (
    <section className="mt-5 rounded-wobble border border-rule bg-card px-4 py-4" aria-labelledby="en-rubric">
      <h2 id="en-rubric" className="type-section">
        How you’re judged
      </h2>
      <p className="mt-0.5 text-sm text-ink-soft">
        {criteria.length} {criteria.length === 1 ? "criterion" : "criteria"} · {total} points total
      </p>
      <ul className="m-0 mt-2 list-none p-0">
        {criteria.map((c) => (
          <li key={c.name} className="border-b border-rule py-2 last:border-b-0">
            <div className="flex items-start justify-between gap-2">
              <p className="font-medium">{c.name}</p>
              <Badge>{Number(c.max_points) || 0} points</Badge>
            </div>
            {c.description && <p className="mt-0.5 text-sm text-ink-soft">{c.description}</p>}
          </li>
        ))}
      </ul>
    </section>
  );
}

function Conflict({
  slug,
  open,
  entry,
  c,
  onClose,
  onSaved,
}: {
  slug: string;
  open: boolean;
  entry: EntryT;
  c: { mine: Record<string, unknown>; newer: EntryT } | null;
  onClose: () => void;
  onSaved: (r: EntryData) => void;
}) {
  const api = useHack();
  const qc = useQueryClient();
  const toast = useToast();
  const [busy, setBusy] = useState(false);
  const rows = c ? conflicts(c.mine, entry, c.newer) : [];
  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()} title="A teammate changed this entry while you were editing">
      {rows.length ? (
        <div className="grid max-h-[50dvh] grid-cols-2 gap-2 overflow-auto">
          {rows.map((r) => (
            <div key={r.label} className="contents">
              <div className="min-w-0 rounded-field border border-rule px-3 py-2">
                <p className="text-sm text-ink-soft">Your {r.label}</p>
                <p className="break-words">{r.mine || "(empty)"}</p>
              </div>
              <div className="min-w-0 rounded-field border border-rule px-3 py-2">
                <p className="text-sm text-ink-soft">Their {r.label}</p>
                <p className="break-words">{r.theirs || "(empty)"}</p>
              </div>
            </div>
          ))}
        </div>
      ) : (
        <p>Nothing in your edit is different from theirs.</p>
      )}
      <DialogActions>
        <Button
          disabled={busy}
          onClick={async () => {
            onClose();
            await qc.invalidateQueries({ queryKey: ["entry", slug] });
          }}
        >
          Take theirs
        </Button>
        <Button
          variant="primary"
          disabled={busy}
          onClick={async () => {
            if (!c) return;
            setBusy(true);
            try {
              const r = (await api.entries.save(slug, { ...c.mine, updated_at: c.newer.updated_at || "" })) as EntryData;
              onClose();
              onSaved(r);
            } catch (err) {
              if (!signedOut(err)) toast(say(err, "Could not save. Try again."), { bad: true });
            } finally {
              setBusy(false);
            }
          }}
        >
          Keep mine
        </Button>
      </DialogActions>
    </Dialog>
  );
}

// ----------------------------------------------------------- read-only ---

function ReadOnly({ slug, data, criteria }: { slug: string; data: EntryData; criteria: Criterion[] }) {
  const d = useEventDetail(slug).data!;
  const ev = evOf(d);
  const team = teamOf(d);
  const entry = data.entry || {};
  const rules = data.rules || {};
  const status = data.status || "draft";
  const card = (incomplete: boolean) => (
    <>
      <EntryCard
        entry={{
          title: entry.title,
          team_name: team?.name || "",
          team_members: (team?.members || [])
            .map((x) => x.display_name)
            .filter(Boolean)
            .join(", "),
          track: entry.track,
          problem: entry.problem,
          tagline: entry.tagline,
          audience: entry.audience,
          tools: entry.tools,
          maturity: entry.maturity,
        }}
        badges={incomplete ? <Badge tone="down">Incomplete</Badge> : undefined}
      />
      <ShowYourWork slug={slug} entry={entry} site={data.site || {}} rules={rules} siteURL={safeHref(team?.site?.url)} />
    </>
  );
  const next = (text: string) => (
    <section className="rounded-wobble border border-rule bg-card px-4 py-4">
      <h2 className="type-section">What happens next</h2>
      <p className="mt-1 text-sm">{text}</p>
    </section>
  );
  const split = (main: ReactNode, side: ReactNode) => (
    <div className="mt-5 grid items-start gap-5 md:grid-cols-[minmax(0,1fr)_20rem]">
      <div className="min-w-0">{main}</div>
      <aside className="min-w-0">
        {side}
        <Rubric criteria={criteria} />
      </aside>
    </div>
  );
  if (status === "submitted") {
    const by = data.submit_auto
      ? "Submitted automatically at the deadline"
      : "Submitted" + (data.submitted_at ? " · " + dateTime(data.submitted_at, ev) : "") + (data.submitted_by ? " by " + data.submitted_by : "");
    return (
      <>
        <Title />
        <StatusBox tone="ok" title={by}>
          Entries are closed. This is what the judges see.
        </StatusBox>
        {split(card(false), next("The judges score every submitted entry. Results show here and on the event page when the organisers publish them."))}
      </>
    );
  }
  if (status === "not_submitted" && hasAnyContent(entry))
    return (
      <>
        <Title />
        <StatusBox title="Entries closed">Not complete. It will still be judged, marked incomplete.</StatusBox>
        {split(card(true), next("The judges score this entry too, with an Incomplete flag. Results show here and on the event page when the organisers publish them."))}
      </>
    );
  if (status === "draft" && data.refusal === "submissions_closed" && hasAnyContent(entry))
    return (
      <>
        <Title />
        <StatusBox tone={data.complete ? "ok" : undefined} title="Entries closed">
          {(data.complete ? "It is being submitted for you automatically." : "Not complete. It will still be judged, marked incomplete.") + " This is what the judges see."}
        </StatusBox>
        {split(card(!data.complete), null)}
      </>
    );
  const closed = (CLOSED[data.refusal || ""] || CLOSED.submissions_closed).replace(/\.+$/, "") + ". This is what the judges see.";
  return (
    <>
      <Title />
      <Notice title={closed} />
      <section className="rounded-wobble border border-rule bg-card px-4 py-4">
        <h2 className="type-section">Your entry</h2>
        <p className="mt-1 text-ink-soft">Nothing was entered by the deadline.</p>
      </section>
      <div className="max-w-md">
        <Rubric criteria={criteria} />
      </div>
    </>
  );
}

function ShowYourWork({ slug, entry, site, rules, siteURL }: { slug: string; entry: EntryT; site: { exists?: boolean; live_version?: number | string | null }; rules: EntryData["rules"]; siteURL: string }) {
  // A line the event switched off is left out unless something is in it.
  const on = (f: string, has: unknown) => !!has || mode(rules, false, f) !== "off";
  const link = (u?: string) =>
    safeHref(u) ? (
      <a href={safeHref(u)} target="_blank" rel="noopener" className="break-all">
        {u}
      </a>
    ) : (
      "(none)"
    );
  const rows: [string, ReactNode][] = [];
  if (mode(rules, false, "site") !== "off" || site.exists)
    rows.push([
      "Website",
      site.exists && site.live_version ? (
        <>
          Published · version {site.live_version}
          {siteURL && (
            <>
              {" · "}
              <a href={siteURL} target="_blank" rel="noopener" className="break-all">
                {siteURL}
              </a>
            </>
          )}
        </>
      ) : (
        "(none)"
      ),
    ]);
  if (on("video_url", entry.video_url)) rows.push(["Video", link(entry.video_url)]);
  if (on("code_url", entry.code_url)) rows.push(["Code", link(entry.code_url)]);
  if (on("screenshot", entry.has_screenshot)) rows.push(["Screenshot", entry.has_screenshot ? <Screenshot path={paths.entryScreenshot(slug)} alt="Screenshot" /> : "(none)"]);
  return (
    <section className="mt-5 rounded-wobble border border-rule bg-card px-4 py-4 md:px-5">
      <h2 className="type-section">Show your work</h2>
      <dl className="mt-2 grid grid-cols-1 gap-x-4 gap-y-1 md:grid-cols-[8rem_minmax(0,1fr)] md:gap-y-2">
        {rows.map(([k, val]) => (
          <div key={k} className="contents">
            <dt className="text-sm text-ink-soft">{k}</dt>
            <dd className="m-0 min-w-0">{val}</dd>
          </div>
        ))}
      </dl>
    </section>
  );
}
