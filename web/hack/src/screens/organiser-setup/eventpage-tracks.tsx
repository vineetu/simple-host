// Tracks: the challenges teams can enter (PUT E/tracks writes the whole
// list). Add and Edit in a dialog; Remove asks first, naming how many teams
// chose it, then offers Undo. Read-only once ended or under inspection.
import { useState, type FormEvent } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Button } from "@kit/components/ui/button";
import { AskDialog, Dialog, DialogActions } from "@kit/components/ui/dialog";
import { EmptyState } from "@kit/components/ui/empty-state";
import { Field, FieldMessage, Input, Textarea } from "@kit/components/ui/input";
import { Menu } from "@kit/components/ui/menu";
import { Notice } from "@kit/components/ui/notice";
import { useToast } from "@kit/components/ui/toast";
import { useHack } from "@/lib/client";
import { errorText } from "./common";
import { slugify } from "./eventpage-data";
import { PlusIcon, TabHead, type TabProps } from "./eventpage-ui";

export type Track = { slug?: string; name: string; challenge?: string; prize?: string };
type TeamRow = { track?: { slug?: string } | null };

function useTracks(p: TabProps) {
  const api = useHack();
  const qc = useQueryClient();
  const toast = useToast();
  const teams = useQuery({ queryKey: ["teams", p.slug], queryFn: () => api.teams.list(p.slug) as Promise<{ teams?: TeamRow[] }> });
  const tracks = ((p.ev.tracks as Track[] | undefined) || []).map((t) => ({ ...t }));
  const countFor = (t: Track) => (teams.data?.teams || []).filter((team) => team.track?.slug && team.track.slug === t.slug).length;
  /** save writes the list; it answers '' or the refusal. */
  const save = async (list: Track[]): Promise<string> => {
    try {
      const payload = list.map((t) => ({ slug: t.slug || slugify(t.name), name: t.name, challenge: t.challenge, prize: t.prize }));
      const r = (await api.content.setTracks(p.slug, { tracks: payload })) as unknown as Track[];
      qc.setQueryData(["event", p.slug], (old: typeof p.d | undefined) => (old ? { ...old, event: { ...old.event, tracks: Array.isArray(r) ? r : list } } : old));
      void qc.invalidateQueries({ queryKey: ["event", p.slug] });
      void qc.invalidateQueries({ queryKey: ["teams", p.slug] });
      return "";
    } catch (e) {
      return errorText(e) || "Could not save tracks";
    }
  };
  return { tracks, countFor, save, toast };
}

function TrackDialog({ existing, onSave, onClose }: { existing: Track | null; onSave: (t: Track) => Promise<string>; onClose: () => void }) {
  const [name, setName] = useState(existing?.name || "");
  const [challenge, setChallenge] = useState(existing?.challenge || "");
  const [prize, setPrize] = useState(existing?.prize || "");
  const [err, setErr] = useState<{ name?: string; form?: string }>({});
  const [busy, setBusy] = useState(false);
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (!name.trim()) return setErr({ name: "Enter a track name." });
    setBusy(true);
    const r = await onSave({ slug: existing ? existing.slug : slugify(name.trim()), name: name.trim(), challenge: challenge.trim(), prize: prize.trim() });
    setBusy(false);
    if (r) setErr({ form: r });
    else onClose();
  };
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()} title={existing ? "Edit track" : "Add track"}>
      <form onSubmit={submit} noValidate>
        <Field id="trk-name" label="Track name" error={err.name}>
          <Input id="trk-name" maxLength={80} value={name} onChange={(e) => setName(e.target.value)} aria-invalid={!!err.name} autoFocus />
        </Field>
        <Field id="trk-challenge" label="Challenge (optional)">
          <Textarea id="trk-challenge" value={challenge} onChange={(e) => setChallenge(e.target.value)} className="min-h-20" />
        </Field>
        <Field id="trk-prize" label="Prize (optional)">
          <Input id="trk-prize" maxLength={80} value={prize} onChange={(e) => setPrize(e.target.value)} />
        </Field>
        {err.form && <FieldMessage tone="bad">{err.form}</FieldMessage>}
        <DialogActions>
          <Button onClick={onClose}>Cancel</Button>
          <Button type="submit" variant="primary" disabled={busy}>
            {existing ? "Save track" : "Add track"}
          </Button>
        </DialogActions>
      </form>
    </Dialog>
  );
}

export function Tracks(p: TabProps) {
  const { tracks, countFor, save, toast } = useTracks(p);
  const [edit, setEdit] = useState<{ i: number | null } | null>(null);
  const [removing, setRemoving] = useState<number | null>(null);
  const readOnly = p.admin || p.ended;
  const add = (
    <Button variant="primary" onClick={() => setEdit({ i: null })}>
      <PlusIcon />
      Add track
    </Button>
  );
  const r = removing == null ? null : tracks[removing];
  const n = r ? countFor(r) : 0;
  return (
    <>
      <TabHead title="Tracks" sub="Challenges teams can enter. Each team picks its own on its Team page." action={!readOnly && tracks.length ? add : undefined} />
      {p.ended && !p.admin && <Notice title="This event has ended. Tracks can’t change any more." />}
      {tracks.length ? (
        <ul aria-label="Tracks" className="mt-4 max-w-prose list-none rounded-field border-[1.5px] border-rule bg-card px-3 py-1">
          {tracks.map((t, i) => {
            const c = countFor(t);
            return (
              <li key={(t.slug || "") + i} className="flex min-h-14 items-center gap-3 border-b border-rule px-1 py-2 last:border-b-0">
                <span className="min-w-0 flex-1">
                  <span className="block font-semibold text-ink">{t.name}</span>
                  <span className="block text-sm text-ink-soft">
                    {t.challenge || ""}
                    {t.prize ? (t.challenge ? " · " : "") + "Prize: " + t.prize : ""}
                  </span>
                </span>
                <span className="flex-none text-sm text-ink-soft">
                  {c} team{c === 1 ? "" : "s"}
                </span>
                {!readOnly && (
                  <div className="flex flex-none items-center gap-2">
                    <Button className="hidden md:inline-flex" onClick={() => setEdit({ i })} aria-label={"Edit " + t.name}>
                      Edit
                    </Button>
                    <Menu
                      label={"More for " + (t.name || "this track")}
                      items={[{ label: "Edit", onSelect: () => setEdit({ i }) }, { separator: true }, { label: "Remove", onSelect: () => setRemoving(i), danger: true }]}
                    />
                  </div>
                )}
              </li>
            );
          })}
        </ul>
      ) : (
        <EmptyState title="No tracks yet" action={readOnly ? undefined : add}>
          Add tracks if teams should pick a challenge. Without tracks, everyone competes together.
        </EmptyState>
      )}
      {edit && (
        <TrackDialog
          existing={edit.i == null ? null : tracks[edit.i]}
          onClose={() => setEdit(null)}
          onSave={(t) => {
            const list = tracks.slice();
            if (edit.i == null) list.push(t);
            else list[edit.i] = t;
            return save(list);
          }}
        />
      )}
      <AskDialog
        open={!!r}
        onOpenChange={(o) => !o && setRemoving(null)}
        title="Remove track?"
        body={r ? (n ? `${n} team${n === 1 ? "" : "s"} chose ${r.name || "this track"}. They’ll have no track. Remove it?` : `Remove ${r.name || "this track"}?`) : ""}
        confirmLabel="Remove"
        danger
        onConfirm={async () => {
          if (removing == null) return;
          const before = tracks.slice();
          const name = tracks[removing].name || "Track";
          const err = await save(tracks.filter((_, i) => i !== removing));
          if (err) return void toast(err, { bad: true });
          toast("Removed " + name, { undo: { label: "Undo", run: () => void save(before).then((e) => e && toast(e, { bad: true })) } });
        }}
      />
    </>
  );
}
