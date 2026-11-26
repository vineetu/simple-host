// Settings → Sign-up (questions and approval, the Applications switch, the
// directory listing) and Settings → Teams (team finding, team size).
// Single switches save as they change and offer Undo; lists and numbers
// have their own Save.
import { useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Button } from "@kit/components/ui/button";
import { Check } from "@kit/components/ui/choice";
import { Dialog, DialogActions } from "@kit/components/ui/dialog";
import { Field, FieldMessage, Input } from "@kit/components/ui/input";
import { Notice } from "@kit/components/ui/notice";
import { useToast } from "@kit/components/ui/toast";
import { useHack } from "@/lib/client";
import { Failed } from "@/app/states";
import type { TabProps } from "./Settings";
import { errorText, Panel, SaveFoot, useRefreshEvent } from "./common";
import { withIds, type Question } from "./settings";
import { locks, SaveRow, submit, TabLoading, useAnchor } from "./settings-ui";
import { useUnsaved } from "@kit/guard/unsaved";
import { LockNote } from "@/shared/locks";

type Registration = { questions?: Question[]; approval_required?: boolean; applications_open?: boolean };
type Application = { user_id: string; status?: string; joined_via?: string };

/** useSwitch saves one event-wide switch on change (PATCH E) and offers Undo. */
function useEventSwitch(slug: string) {
  const api = useHack();
  const toast = useToast();
  const refresh = useRefreshEvent(slug);
  return async (field: string, on: boolean, setErr: (e: string) => void) => {
    setErr("");
    try {
      await api.event.update(slug, { [field]: on });
      await refresh();
      toast("Saved", {
        undo: {
          label: "Undo",
          run: () =>
            void api.event
              .update(slug, { [field]: !on })
              .then(refresh)
              .catch((e) => toast(errorText(e), { bad: true })),
        },
      });
    } catch (e) {
      setErr(errorText(e));
    }
  };
}

function Questions({ slug, reg, failed, locked, applicationsOpen }: { slug: string; reg?: Registration; failed: boolean; locked: boolean; applicationsOpen: boolean }) {
  const api = useHack();
  const qc = useQueryClient();
  const toast = useToast();
  const saved = reg?.questions || [];
  const [draft, setDraft] = useState<Question[]>(saved);
  const [approval, setApproval] = useState(!!reg?.approval_required);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [approvalErr, setApprovalErr] = useState("");
  const [waiting, setWaiting] = useState<number | null>(null);
  const apps = useQuery({ queryKey: ["applications", slug], queryFn: async () => ((await api.people.applications(slug)) as unknown as Application[]) || [], enabled: !locked });
  useEffect(() => {
    setDraft(reg?.questions || []);
    setApproval(!!reg?.approval_required);
  }, [reg]);
  const dirty = JSON.stringify(draft) !== JSON.stringify(saved);
  useUnsaved(dirty);
  const off = locked || failed;
  const set = (i: number, q: Partial<Question>) => setDraft(draft.map((x, j) => (j === i ? { ...x, ...q } : x)));

  const save = async () => {
    if (draft.some((q) => !q.prompt.trim())) return setError("Write each question, or remove the empty one.");
    setBusy(true);
    setError("");
    try {
      const r = (await api.people.setRegistration(slug, { questions: withIds(draft), approval_required: approval })) as Registration;
      qc.setQueryData(["registration", slug], r);
      toast("Questions saved.");
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(false);
    }
  };
  const putApproval = async (on: boolean, admit: boolean) => {
    setApproval(on);
    setApprovalErr("");
    try {
      const r = (await api.people.setRegistration(slug, { questions: saved, approval_required: on, admit_pending: admit })) as Registration;
      qc.setQueryData(["registration", slug], { ...r, questions: r.questions || saved });
      if (admit) void qc.invalidateQueries({ queryKey: ["applications", slug] });
      toast(admit ? "Saved. Everyone waiting is approved." : "Saved");
    } catch (e) {
      setApproval(!on);
      setApprovalErr(errorText(e));
    }
  };
  const onApproval = (on: boolean) => {
    // Only people who joined with the link can be let in at once; applications always wait.
    const pending = (apps.data || []).filter((a) => a.status === "pending" && a.joined_via !== "application").length;
    if (!on && pending) setWaiting(pending);
    else void putApproval(on, false);
  };

  return (
    <Panel id="signup" title="Sign-up" sub="Who has to wait for you, and what you ask people when they join.">
      {failed && <Notice tone="bad" title="Sign-up questions could not be loaded.">Saving here is turned off until you reload the page.</Notice>}
      <Check
        checked={approval}
        disabled={off}
        onChange={onApproval}
        hint={
          <>
            Applications from Find a hackathon and your event page always wait for you, whatever this says. Applications are {applicationsOpen ? "on" : "off"}:{" "}
            <a href="#applications">change it under Applications below</a>.
          </>
        }
      >
        Approve people who join with your link before they can join a team or publish
      </Check>
      {approvalErr && <FieldMessage tone="bad">{approvalErr}</FieldMessage>}
      <form onSubmit={submit(save)} className="mt-4">
        <h3 className="type-label text-ink-soft">Questions</h3>
        <p className="mt-1 text-sm text-ink-soft">Applicants also get three optional fields of ours: where they’re based, age range and gender.</p>
        {draft.length === 0 && <p className="mt-3 text-ink-soft">No questions yet. People join with just their name.</p>}
        <ul className="m-0 mt-3 list-none p-0">
          {draft.map((q, i) => (
            <li key={i} className="mb-3 rounded-field border border-rule px-3 py-2">
              <Input aria-label={"Question " + (i + 1)} placeholder="Question" maxLength={160} value={q.prompt} disabled={off} onChange={(e) => set(i, { prompt: e.target.value })} />
              <div className="mt-1 flex flex-wrap items-center gap-2">
                <div className="flex-1">
                  <Check checked={q.required} disabled={off} onChange={(on) => set(i, { required: on })}>
                    Required
                  </Check>
                </div>
                <Button variant="danger" disabled={off} onClick={() => setDraft(draft.filter((_, j) => j !== i))} aria-label={"Remove question " + (i + 1)}>
                  Remove
                </Button>
              </div>
            </li>
          ))}
        </ul>
        <SaveRow primary label="Save questions" busy={busy} disabled={off || !dirty} error={error} note={draft.length >= 8 ? "Up to 8 questions." : undefined}>
          <Button disabled={off || draft.length >= 8} onClick={() => setDraft([...draft, { id: "", prompt: "", required: false }])}>
            Add question
          </Button>
        </SaveRow>
      </form>
      <Dialog
        open={waiting !== null}
        onOpenChange={(o) => !o && setWaiting(null)}
        title="Waiting applications"
        description={
          (waiting === 1
            ? "1 person who joined with your link is still waiting. Approve them now, or leave them pending. You can still approve or reject them later from People."
            : `${waiting} people who joined with your link are still waiting. Approve all of them now, or leave them pending. You can still approve or reject each one later from People.`) +
          " Applications always wait for your decision."
        }
      >
        <DialogActions>
          <Button onClick={() => setWaiting(null)}>Cancel</Button>
          <Button
            onClick={() => {
              setWaiting(null);
              void putApproval(false, false);
            }}
          >
            Leave them pending
          </Button>
          <Button
            variant="primary"
            onClick={() => {
              setWaiting(null);
              void putApproval(false, true);
            }}
          >
            Approve all now
          </Button>
        </DialogActions>
      </Dialog>
    </Panel>
  );
}

export function SignupTab({ d }: TabProps) {
  const api = useHack();
  const qc = useQueryClient();
  const toast = useToast();
  const slug = d.event.slug;
  const ev = d.event as typeof d.event & { applications_open?: boolean };
  const { settings: lock, admin } = locks(d);
  const reg = useQuery({ queryKey: ["registration", slug], queryFn: () => api.people.registration(slug) as Promise<Registration> });
  const voting = useQuery({ queryKey: ["voting", slug], queryFn: () => api.voting.settings(slug) as Promise<{ directory_listed?: boolean }> });
  const saveSwitch = useEventSwitch(slug);
  const [appsErr, setAppsErr] = useState("");
  const [dirErr, setDirErr] = useState("");
  useAnchor(!reg.isPending && !voting.isPending);
  if (reg.isPending || voting.isPending) return <TabLoading />;
  const listed = !!voting.data?.directory_listed;
  const setListed = async (on: boolean, undo = true) => {
    setDirErr("");
    try {
      const r = (await api.voting.setDirectory(slug, { listed: on })) as { listed?: boolean };
      qc.setQueryData(["voting", slug], { ...voting.data, directory_listed: !!r.listed });
      if (undo) toast("Saved", { undo: { label: "Undo", run: () => void setListed(!on, false) } });
    } catch (e) {
      setDirErr(errorText(e));
    }
  };
  const applicationsOpen = ev.applications_open !== false;
  return (
    <>
      <LockNote reason={lock} />
      <Questions slug={slug} reg={reg.data} failed={reg.isError} locked={!!lock} applicationsOpen={applicationsOpen} />
      <Panel id="applications" title="Applications">
        <Check checked={applicationsOpen} disabled={!!lock} onChange={(on) => void saveSwitch("applications_open", on, setAppsErr)} hint="When your event is listed in Find a hackathon, an Apply button shows there and on your event page while sign-up is open and during Building. Every application waits for you in People → Applications.">
          Let people apply without your join link
        </Check>
        {appsErr && <FieldMessage tone="bad">{appsErr}</FieldMessage>}
        <p className="mt-2 text-sm text-ink-soft">Your join link keeps working either way and stays private. Turning this off stops new applications. Ones already waiting stay in People.</p>
        <p className="mt-2 text-sm text-ink-soft">
          {listed ? (
            <>
              Listed in Find a hackathon: yes. <a href="#directory">Change below</a>.
            </>
          ) : (
            <>
              Not listed in Find a hackathon, so nobody can apply yet. Applications start once you <a href="#directory">list the event below</a>.
            </>
          )}
        </p>
      </Panel>
      <Panel id="directory" title="Directory">
        {voting.isError ? (
          <Failed error={voting.error} retry={() => voting.refetch()} />
        ) : (
          <Check checked={listed} disabled={!!admin} onChange={(on) => void setListed(on)} hint="Anyone can find the event and its public page there.">
            List this event on Find a hackathon
          </Check>
        )}
        {dirErr && <FieldMessage tone="bad">{dirErr}</FieldMessage>}
      </Panel>
    </>
  );
}

export function TeamsTab({ d }: TabProps) {
  const slug = d.event.slug;
  const ev = d.event as typeof d.event & { team_finding?: boolean; team_size_max?: number };
  const { settings: lock } = locks(d);
  const api = useHack();
  const toast = useToast();
  const refresh = useRefreshEvent(slug);
  const saveSwitch = useEventSwitch(slug);
  const savedSize = String(ev.team_size_max ?? "");
  const [size, setSize] = useState(savedSize);
  const [err, setErr] = useState("");
  const [sizeErr, setSizeErr] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => setSize(savedSize), [savedSize]);
  const dirty = size !== savedSize;
  useUnsaved(dirty);
  const save = async () => {
    const n = Number(size);
    if (!Number.isInteger(n) || n < 1 || n > 50) return setSizeErr("Most people per team must be between 1 and 50.");
    setSizeErr("");
    setBusy(true);
    try {
      await api.event.update(slug, { team_size_max: n });
      await refresh();
      toast("Saved");
    } catch (e) {
      setSizeErr(errorText(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <>
      <LockNote reason={lock} />
      <Panel title="Team finding" sub="How teams form in this event.">
        <Check checked={!!ev.team_finding} disabled={!!lock} onChange={(on) => void saveSwitch("team_finding", on, setErr)} hint="Participants can browse teams, ask to join and invite people looking for a team. Team links and codes work either way.">
          Team finding
        </Check>
        {err && <FieldMessage tone="bad">{err}</FieldMessage>}
      </Panel>
      <Panel title="Team size">
        <form onSubmit={submit(save)} noValidate>
          <Field id="ts-size" label="Most people per team" error={sizeErr}>
            <Input id="ts-size" type="number" inputMode="numeric" min={1} max={50} value={size} disabled={!!lock} onChange={(e) => setSize(e.target.value)} className="max-w-40" aria-invalid={!!sizeErr} />
          </Field>
          <SaveFoot dirty={dirty} busy={busy} disabled={!!lock} onDiscard={() => setSize(savedSize)} />
        </form>
      </Panel>
    </>
  );
}
