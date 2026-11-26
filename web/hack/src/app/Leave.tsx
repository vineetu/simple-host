// Leave this event: one flow for every entry point (the old leave.js, same
// words). It always reads a fresh preview (GET E/leave) before it asks, draws
// the case the server names, and on a stale refusal reads it again and asks
// again with a note. After leaving it goes to Your events with a toast.
// (End is shown but cannot be chosen when nobody joined: its button stays off.)
import { useState } from "react";
import { Dialog, DialogActions } from "@kit/components/ui/dialog";
import { Button } from "@kit/components/ui/button";
import { RadioCards } from "@kit/components/ui/choice";
import { Field, Input } from "@kit/components/ui/input";
import { useToast } from "@kit/components/ui/toast";
import { ApiError } from "@kit/api/call";
import { useHack, type LeavePreview } from "@/lib/client";
import { joinNames, possessive } from "@/lib/labels";
import { flash } from "@/lib/session";
import { href } from "@/routes/resolve";

const STALE = ["leave_state_changed", "last_organiser", "successor_required", "invalid_successor", "archive_needs_participants"];

function teammates(team: NonNullable<LeavePreview["team"]>) {
  const names = (team.others || []).slice();
  const extra = Math.max(0, (team.others_count || names.length) - names.length);
  if (extra > 0) names.push(extra === 1 ? "1 other" : extra + " others");
  const total = team.others_count != null ? team.others_count : (team.others || []).length;
  return joinNames(names) + " " + (total === 1 ? "keeps" : "keep");
}

/** participantWarning is the participant case's warning line (shared with a screen's danger-zone row). */
export function participantWarning(p: LeavePreview): string {
  const team = p.team;
  if (!team)
    return p.rejoin === "join"
      ? "You'll lose access to the event. You can join again while sign-up is open."
      : "You'll lose access to the event. Sign-up has closed, so you can't join again.";
  if (team.frozen) return `You'll lose access to the event. ${possessive(team.name)} entry and site stay as they are, and you won't be listed on the team any more.`;
  if (team.deleted_on_leave) return `You're the only member of ${team.name}, so the team, its entry and its website will be deleted for good.`;
  return `You'll leave ${team.name} and lose access to the event. ${teammates(team)} the team, its entry and its website.`;
}

type Ask = { title: string; lines: { text: string; muted?: boolean }[]; confirm: string };

/** ask is the simple cases' dialog content (participant, applicant, judge, co-organiser), or null for the form cases. */
export function ask(p: LeavePreview, title: string): Ask | null {
  switch (p.case) {
    case "participant": {
      const t = p.team;
      if (!t) return { title: `Leave ${title}?`, lines: [{ text: participantWarning(p) }], confirm: "Leave event" };
      if (t.frozen) return { title: `Leave ${title}?`, lines: [{ text: participantWarning(p) }, { text: "You can't rejoin this team.", muted: true }], confirm: "Leave event" };
      if (t.deleted_on_leave)
        return {
          title: `Leave ${title}?`,
          lines: [{ text: participantWarning(p) }, { text: "You'll lose access to the event. You can join again while sign-up is open.", muted: true }],
          confirm: "Delete team and leave",
        };
      return {
        title: `Leave ${title}?`,
        lines: [{ text: participantWarning(p) }, { text: `You can join again while sign-up is open, but not as part of ${t.name} unless they let you back in.`, muted: true }],
        confirm: "Leave event",
      };
    }
    case "applicant":
      return p.approval_status === "rejected"
        ? { title: `Leave ${title}?`, lines: [{ text: "It will be removed from your Home. You can apply again while the event takes applications, and the organisers decide again." }], confirm: "Leave event" }
        : { title: "Withdraw your application?", lines: [{ text: `Your application to ${title} and your answers will be removed. You can apply again while sign-up is open.` }], confirm: "Withdraw application" };
    case "judge": {
      const n = p.scored_teams || 0;
      return {
        title: `Leave ${title}?`,
        lines: [
          { text: n > 0 ? `You'll lose access to judging. Your scores for ${n} team${n === 1 ? "" : "s"} stay and still count in the results.` : "You'll lose access to judging." },
          { text: p.rejoin === "judge_link" ? "You can come back with the judge link until results are published." : "Results are out, so you can't rejoin as a judge.", muted: true },
        ],
        confirm: "Leave event",
      };
    }
    case "co_organiser": {
      const staying = (p.organisers_staying || []).filter(Boolean);
      return {
        title: `Leave ${title}?`,
        lines: [
          { text: `You'll stop being an organiser and lose access to everything here. ${joinNames(p.organisers_staying)} ${staying.length === 1 ? "keeps" : "keep"} running it.` },
          { text: "To come back, ask an organiser for a new co-organiser invite.", muted: true },
        ],
        confirm: "Leave event",
      };
    }
  }
  return null;
}

/** doneText is the toast Your events shows afterwards. */
export function doneText(p: LeavePreview, title: string, data: { event?: string; successor?: { display_name: string } }): string {
  if (p.case === "applicant") return p.approval_status === "rejected" ? `You left ${title}.` : `You withdrew your application to ${title}.`;
  if (p.case === "creator" && data.successor) return `You handed ${title} to ${data.successor.display_name} and left it.`;
  if (data.event === "ended") return `You ended ${title} and left it.`;
  if (data.event === "deleted") return `${title} was deleted.`;
  return `You left ${title}.`;
}

type State = { preview: LeavePreview; warn?: string } | null;

/** useLeave returns [open(slug, title), the dialog to render]. */
export function useLeave(): [(slug: string, title: string) => void, React.ReactNode] {
  const api = useHack();
  const toast = useToast();
  const [ev, setEv] = useState<{ slug: string; title: string } | null>(null);
  const [state, setState] = useState<State>(null);
  const [blocked, setBlocked] = useState<string | null>(null);
  const [choice, setChoice] = useState("");
  const [typed, setTyped] = useState("");
  const [busy, setBusy] = useState(false);

  const load = async (slug: string, title: string, warn?: string) => {
    setEv({ slug, title });
    try {
      const preview = await api.event.leavePreview(slug);
      if (preview.blocked) {
        setBlocked(preview.blocked.message || "This event can't be left right now.");
        return;
      }
      if (!preview.case || !["participant", "applicant", "judge", "co_organiser", "creator", "last_organiser"].includes(preview.case)) {
        toast("This event can't be left right now.", { bad: true });
        return;
      }
      const endBlocked = !!preview.close_options?.end?.blocked;
      setChoice(preview.case === "creator" ? preview.successors?.[0]?.user_id || "" : preview.case === "last_organiser" ? (endBlocked ? "delete" : "end") : "");
      setTyped("");
      setState({ preview, warn });
    } catch (e) {
      if ((e as ApiError).status !== 401) toast((e as Error).message, { bad: true });
    }
  };

  const close = () => setState(null);
  const go = async () => {
    if (!state || !ev) return;
    const p = state.preview;
    const extra = p.case === "creator" ? { successor_user_id: choice } : p.case === "last_organiser" ? { close: choice } : {};
    setBusy(true);
    try {
      const data = await api.event.leave(ev.slug, { confirm: ev.slug, ...extra });
      flash(doneText(p, ev.title, data));
      location.assign("/events");
    } catch (e) {
      const err = e as ApiError;
      if (STALE.includes(err.code)) await load(ev.slug, ev.title, "Something changed while this was open. Check the details again.");
      else if (err.status !== 401) {
        close();
        toast(err.message, { bad: true });
      }
    } finally {
      setBusy(false);
    }
  };

  let dialog: React.ReactNode = null;
  if (blocked && ev)
    dialog = (
      <Dialog open onOpenChange={() => setBlocked(null)} title={`Leave ${ev.title}?`} description={blocked}>
        <DialogActions>
          <Button variant="primary" onClick={() => setBlocked(null)}>
            OK
          </Button>
        </DialogActions>
      </Dialog>
    );
  else if (state && ev) {
    const p = state.preview;
    const simple = ask(p, ev.title);
    const warn = state.warn && (
      <p role="alert" className="mb-3 rounded-field border border-bad px-3 py-2 text-sm text-bad">
        {state.warn}
      </p>
    );
    if (simple)
      dialog = (
        <Dialog open onOpenChange={(o) => !o && close()} title={simple.title}>
          {warn}
          {simple.lines.map((l, i) => (
            <p key={i} className={l.muted ? "mt-2 text-sm text-ink-soft" : "mt-2"}>
              {l.text}
            </p>
          ))}
          <DialogActions>
            <Button onClick={close}>Cancel</Button>
            <Button variant="danger" onClick={go} disabled={busy}>
              {simple.confirm}
            </Button>
          </DialogActions>
        </Dialog>
      );
    else if (p.case === "creator") {
      const successors = p.successors || [];
      dialog = (
        <Dialog
          open
          onOpenChange={(o) => !o && close()}
          title={`Hand over ${ev.title}`}
          description="You created this event, so choose who takes it over before you leave. They become its main organiser, and nobody can remove them from it."
        >
          {warn}
          <RadioCards
            label="Who takes it over"
            value={choice}
            onChange={setChoice}
            options={successors.map((s) => ({ value: s.user_id, title: s.display_name, hint: "Co-organiser" + (s.email ? " · " + s.email : "") }))}
          />
          <p className="mt-3 text-sm text-ink-soft">You'll lose access to managing the event. To come back, ask them for a co-organiser invite.</p>
          <DialogActions>
            <Button onClick={close}>Cancel</Button>
            <Button variant="danger" onClick={go} disabled={busy || !successors.length || !choice}>
              Hand over and leave
            </Button>
          </DialogActions>
        </Dialog>
      );
    } else {
      const endBlocked = !!p.close_options?.end?.blocked;
      const del = choice === "delete";
      dialog = (
        <Dialog open onOpenChange={(o) => !o && close()} title="You're the only organiser" description={`Nobody would be left to run ${ev.title} if you leave, so leaving closes it. Choose how.`}>
          {warn}
          <RadioCards
            label="How to close it"
            value={choice}
            onChange={setChoice}
            options={[
              {
                value: "end",
                title: "End the event",
                hint: endBlocked ? "Nobody joined, so there's nothing to keep. Delete it instead." : "The public page and results stay up as a record. Nothing can change after this, and you can't come back to manage it.",
              },
              { value: "delete", title: "Delete the event", hint: "The public page, results, team sites, entries, scores and votes go for good. There is no undo." },
            ]}
          />
          {del && (
            <div className="mt-3">
              <Field id="leave-confirm" label={`Type ${p.slug || ev.slug} to confirm`}>
                <Input id="leave-confirm" className="font-mono" autoComplete="off" value={typed} onChange={(e) => setTyped(e.target.value)} />
              </Field>
            </div>
          )}
          <p className="mt-3 text-sm text-ink-soft">
            Want someone else to run it? Cancel and invite a co-organiser from <a href={href("organiser", ev.slug, "people")}>People</a> first.
          </p>
          <DialogActions>
            <Button onClick={close}>Cancel</Button>
            <Button variant="danger" onClick={go} disabled={busy || (del ? typed.trim() !== (p.slug || ev.slug) : endBlocked)}>
              {del ? "Delete event and leave" : "End event and leave"}
            </Button>
          </DialogActions>
        </Dialog>
      );
    }
  }
  return [(slug, title) => void load(slug, title), dialog];
}
