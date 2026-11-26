// Moving the event on, from Home and Results: the one next step's confirm,
// Change stage, End event, Publish results (with tie places), and after any
// of them "Tell participants?" (SIM-341). One Radix dialog at a time; each
// step opens the next only once the call before it has worked.
import { useState, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { AskDialog, Dialog, DialogActions } from "@kit/components/ui/dialog";
import { Button } from "@kit/components/ui/button";
import { Check } from "@kit/components/ui/choice";
import { Field, Input, Label, NativeSelect, Textarea } from "@kit/components/ui/input";
import { useToast } from "@kit/components/ui/toast";
import { useHack } from "@/lib/client";
import { stageLabel } from "@/lib/labels";
import { errText, useRefreshEvent } from "./common";
import {
  END_CONFIRM,
  isBack,
  postedLine,
  publishedRows,
  rankText,
  savedPlace,
  scoreText,
  setupLines,
  stageChoices,
  stageEffect,
  suggestedAnnouncement,
  ties,
  type Coverage,
  type JudgingSettings,
  type OrgView,
  type Standing,
} from "./data-overview";

type Open =
  | null
  | { kind: "confirm"; title: string; body: ReactNode; confirm: string; stage: string }
  | { kind: "picker" }
  | { kind: "end" }
  | { kind: "publish"; text?: string }
  | { kind: "announce"; move: string; then?: () => Promise<void> };

/** SetupSummary is the judging setup, read only, each line linking where it is changed (before Start judging). */
function SetupSummary({ slug, onShort }: { slug: string; onShort?: (short: boolean) => void }) {
  const api = useHack();
  const q = useQuery({
    queryKey: ["judging-setup", slug],
    queryFn: async () => {
      const [s, dash] = await Promise.all([api.judging.settings(slug), api.judging.dashboard(slug)]);
      const r = setupLines(slug, s as JudgingSettings, (dash as { coverage: Coverage }).coverage);
      onShort?.(r.short);
      return r;
    },
  });
  if (q.isPending) return <p className="mt-3 text-sm text-ink-soft">Loading the judging setup…</p>;
  if (q.isError) return <p className="mt-3 text-sm text-bad">Could not load judging setup.</p>;
  return (
    <ul className="mt-3 list-none space-y-1 p-0" aria-label="Judging setup">
      {q.data.lines.map(([label, to]) => (
        <li key={label}>
          <a href={to} className="inline-flex min-h-11 items-center">
            {label}
          </a>
        </li>
      ))}
    </ul>
  );
}

function StagePicker({ d, onClose, onPick }: { d: OrgView; onClose: () => void; onPick: (stage: string) => Promise<void> }) {
  const ev = d.event;
  const { choices, initial } = stageChoices(ev.stage, ev.stages_offered as string[] | undefined);
  const [target, setTarget] = useState(initial);
  const [short, setShort] = useState<boolean | null>(null);
  const [busy, setBusy] = useState(false);
  const judging = target === "judging";
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()} title="Change stage">
      <Label htmlFor="stage-pick">New stage</Label>
      <NativeSelect
        id="stage-pick"
        className="w-full"
        value={target}
        autoFocus
        onChange={(e) => {
          setTarget(e.target.value);
          setShort(null);
        }}
      >
        {choices.map((s) => (
          <option key={s} value={s}>
            {stageLabel(s)}
          </option>
        ))}
      </NativeSelect>
      <p className="mt-3 rounded-field border-[1.5px] border-accent bg-accent-soft px-3 py-2 text-sm" role="status">
        {stageEffect(ev.stage, target)}
      </p>
      {judging && <SetupSummary key={target} slug={ev.slug} onShort={setShort} />}
      <DialogActions>
        <Button onClick={onClose}>Cancel</Button>
        <Button
          variant="primary"
          disabled={busy || (judging && short === null)}
          onClick={async () => {
            setBusy(true);
            try {
              await onPick(target);
            } finally {
              setBusy(false);
            }
          }}
        >
          {judging ? (short ? "Start anyway" : "Start judging") : "Continue"}
        </Button>
      </DialogActions>
    </Dialog>
  );
}

/** Announce is "Tell participants?": a suggested post the organiser can edit, email optional. */
function Announce({ d, move, onDone }: { d: OrgView; move: string; onDone: () => void }) {
  const api = useHack();
  const toast = useToast();
  const back = isBack(d.event.stage, move) && move !== "publish_results";
  const s = suggestedAnnouncement(move, back)!;
  const emailable = d.organiser?.counts?.emailable_participants ?? 0;
  const [title, setTitle] = useState(s.title);
  const [body, setBody] = useState(s.body);
  const [email, setEmail] = useState(false);
  const [errs, setErrs] = useState<{ title?: string; body?: string; form?: string }>({});
  const [busy, setBusy] = useState(false);
  const post = async () => {
    const t = title.trim();
    const b = body.trim();
    const e = { title: t ? undefined : "Title is required.", body: b ? undefined : "Message is required." };
    setErrs(e);
    if (e.title || e.body) return;
    setBusy(true);
    try {
      const r = (await api.messages.post(d.event.slug, { title: t, body: b, email_participants: email })) as { emails_queued?: number; organiser_copied?: boolean };
      const line = postedLine(email, r);
      toast(line.text, { bad: line.bad });
      onDone();
    } catch (err) {
      setErrs({ form: errText(err) });
    } finally {
      setBusy(false);
    }
  };
  return (
    <Dialog open onOpenChange={(o) => !o && onDone()} title="Tell participants?" description="Post an announcement about this change. You can edit it first.">
      <form
        noValidate
        onSubmit={(e) => {
          e.preventDefault();
          void post();
        }}
      >
        <Field id="sa-title" label="Title" error={errs.title}>
          <Input id="sa-title" maxLength={120} value={title} aria-invalid={!!errs.title} aria-describedby="sa-title-msg" onChange={(e) => setTitle(e.target.value)} />
        </Field>
        <Field id="sa-body" label="Message" error={errs.body}>
          <Textarea id="sa-body" rows={4} maxLength={3000} value={body} aria-invalid={!!errs.body} aria-describedby="sa-body-msg" onChange={(e) => setBody(e.target.value)} />
        </Field>
        <Check checked={email} disabled={!emailable} onChange={setEmail} hint={emailable ? "Editing it later never sends another email." : "No participant has an email address yet."}>
          Email {emailable} participants
        </Check>
        {errs.form && (
          <p role="alert" className="mt-2 text-sm text-bad">
            {errs.form}
          </p>
        )}
        <DialogActions>
          <Button variant="primary" className="px-6" autoFocus onClick={onDone}>
            Don’t post
          </Button>
          <Button type="submit" disabled={busy}>
            Post
          </Button>
        </DialogActions>
      </form>
    </Dialog>
  );
}

/** Publish is the standings as they stand, tie places to give, and Publish (again). */
function Publish({ d, text, onClose, onPublished }: { d: OrgView; text?: string; onClose: () => void; onPublished: () => Promise<void> }) {
  const api = useHack();
  const toast = useToast();
  const slug = d.event.slug;
  const q = useQuery({
    queryKey: ["standings", slug],
    queryFn: async () => {
      const [pub, prev] = await Promise.all([api.voting.results(slug), api.judging.preview(slug)]);
      return { results: publishedRows(pub), rows: ((prev as { results?: Standing[] }).results || []) as Standing[] };
    },
  });
  const [places, setPlaces] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  if (q.isError)
    return (
      <Dialog open onOpenChange={(o) => !o && onClose()} title="Publish results?">
        <p role="alert" className="text-bad">
          {errText(q.error)}
        </p>
        <DialogActions>
          <Button onClick={onClose}>Close</Button>
        </DialogActions>
      </Dialog>
    );
  if (q.isPending) return null;
  const { results, rows } = q.data;
  const published = !!d.event.results_published || !!results?.length;
  const label = published ? "Publish again" : "Publish results";
  const groups = ties(rows);
  const place = (r: Standing, g: Standing[]) => places[r.team_id] ?? String(savedPlace(r, g, results) ?? r.rank);
  const go = async () => {
    setBusy(true);
    try {
      await api.voting.publish(slug, { rank_overrides: groups.flatMap(([, g]) => g.map((r) => ({ team_id: r.team_id, rank: Number(place(r, g)) }))) });
      await onPublished();
    } catch (e) {
      toast(errText(e), { bad: true });
      onClose();
    } finally {
      setBusy(false);
    }
  };
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()} title={label + "?"} description={text || "This replaces the public snapshot with the standings right now. Continue?"}>
      <table className="w-full border-collapse text-[15px] tabular-nums" aria-label="Standings">
        <thead>
          <tr>
            {["Rank", "Team", "Score"].map((h) => (
              <th key={h} className="border-b border-rule bg-paper-lo px-2 py-1 text-left type-label text-ink-soft">
                {h}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => (
            <tr key={r.team_id}>
              <td className="border-b border-rule px-2 py-1.5">{rankText(r)}</td>
              <td className="border-b border-rule px-2 py-1.5">{r.team_name || "A team"}</td>
              <td className="border-b border-rule px-2 py-1.5">{scoreText(r)}</td>
            </tr>
          ))}
        </tbody>
      </table>
      {groups.length > 0 && <p className="mt-4">Some teams tie. Give each one a final place:</p>}
      {groups.map(([rank, g]) => (
        <fieldset key={rank} className="mt-2 border-0 p-0">
          <legend className="text-sm text-ink-soft">Tied for #{rank}:</legend>
          {g.map((r) => (
            <div key={r.team_id} className="mt-2 grid grid-cols-[1fr_6rem] items-center gap-3">
              <Label htmlFor={"pub-tie-" + r.team_id} className="mb-0 text-base text-ink">
                {r.team_name || "A team"}
              </Label>
              <Input id={"pub-tie-" + r.team_id} type="number" min={1} step={1} value={place(r, g)} onChange={(e) => setPlaces((p) => ({ ...p, [r.team_id]: e.target.value }))} />
            </div>
          ))}
        </fieldset>
      ))}
      <DialogActions>
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="primary" disabled={busy} onClick={go}>
          {label}
        </Button>
      </DialogActions>
    </Dialog>
  );
}

/**
 * useMoves returns the actions Home and Results start (confirm the next
 * move, change stage, end, publish) and the dialog showing now.
 */
export function useMoves(d: OrgView) {
  const api = useHack();
  const toast = useToast();
  const slug = d.event.slug;
  const refresh = useRefreshEvent(slug);
  const [open, setOpen] = useState<Open>(null);
  const close = () => setOpen(null);
  // Nobody to tell when nobody has joined; never after a move back.
  const offer = (move: string, back: boolean) => !back && (d.organiser?.counts?.participants ?? 0) > 0 && !!suggestedAnnouncement(move, back);
  const after = async (move: string, back: boolean) => {
    await refresh(["standings", slug], ["results-screen", slug]);
    setOpen(offer(move, back) ? { kind: "announce", move } : null);
  };
  const stage = async (target: string) => {
    try {
      await api.event.setStage(slug, target);
    } catch (e) {
      toast(errText(e), { bad: true });
      setOpen(null);
      await refresh();
      return;
    }
    await after(target, isBack(d.event.stage, target));
  };
  const archive = async () => {
    try {
      await api.event.setStage(slug, "archived");
    } catch (e) {
      toast(errText(e), { bad: true });
    }
    setOpen(null);
    await refresh();
  };

  let dialog: ReactNode = null;
  if (open?.kind === "confirm")
    dialog = (
      <Dialog open onOpenChange={(o) => !o && close()} title={open.title}>
        {open.body}
        <ConfirmActions label={open.confirm} onCancel={close} onConfirm={() => stage(open.stage)} />
      </Dialog>
    );
  else if (open?.kind === "picker") dialog = <StagePicker d={d} onClose={close} onPick={stage} />;
  else if (open?.kind === "end")
    dialog = (
      <AskDialog
        open
        onOpenChange={(o) => !o && close()}
        title={END_CONFIRM.title}
        body={END_CONFIRM.body}
        confirmLabel={END_CONFIRM.confirm}
        danger
        onConfirm={async () => {
          // The server refuses an announcement on an ended event, so the offer comes first.
          if (offer("archived", false)) setTimeout(() => setOpen({ kind: "announce", move: "archived", then: archive }));
          else await archive();
        }}
      />
    );
  else if (open?.kind === "publish")
    dialog = <Publish d={d} text={open.text} onClose={close} onPublished={() => (d.event.stage !== "archived" ? after("publish_results", false) : refresh().then(close))} />;
  else if (open?.kind === "announce") {
    const then = open.then;
    dialog = <Announce d={d} move={open.move} onDone={() => (then ? void then() : close())} />;
  }

  return {
    dialog,
    confirm: (o: { title: string; body: ReactNode; confirm: string; stage: string }) => setOpen({ kind: "confirm", ...o }),
    picker: () => setOpen({ kind: "picker" }),
    end: () => setOpen({ kind: "end" }),
    publish: (text?: string) => setOpen({ kind: "publish", text }),
  };
}

function ConfirmActions({ label, onCancel, onConfirm }: { label: string; onCancel: () => void; onConfirm: () => Promise<void> }) {
  const [busy, setBusy] = useState(false);
  return (
    <DialogActions>
      <Button onClick={onCancel}>Cancel</Button>
      <Button
        variant="primary"
        disabled={busy}
        onClick={async () => {
          setBusy(true);
          try {
            await onConfirm();
          } finally {
            setBusy(false);
          }
        }}
      >
        {label}
      </Button>
    </DialogActions>
  );
}

export { SetupSummary };
