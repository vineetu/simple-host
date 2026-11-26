// Message a team, or up to 25 (the old _team-message-dialog.js): one
// textarea with a 1000-character counter, Send. A refused send (rate limit,
// event closed) keeps the dialog open with the server's words. The thread
// rows (both directions, newest first) are here too, for Team detail.
import { useState } from "react";
import { Dialog, DialogActions } from "@kit/components/ui/dialog";
import { Button } from "@kit/components/ui/button";
import { FieldMessage, Label, Textarea } from "@kit/components/ui/input";
import { useHack } from "@/lib/client";
import { dateTime } from "@/lib/time";
import { errText } from "./common";
import { agoText, MAX_MESSAGE, sentTo, type Message, type Sent } from "./data-teams";

export function TeamMessageDialog({
  slug,
  open,
  onOpenChange,
  teamIds,
  title,
  onSent,
}: {
  slug: string;
  open: boolean;
  onOpenChange: (o: boolean) => void;
  teamIds: string[];
  title: string;
  /** onSent gets how many people the message reached. */
  onSent: (people: number) => void;
}) {
  const api = useHack();
  const [text, setText] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const close = (o: boolean) => {
    if (!o) {
      setText("");
      setErr("");
    }
    onOpenChange(o);
  };
  return (
    <Dialog open={open} onOpenChange={close} title={title}>
      <form
        noValidate
        onSubmit={async (e) => {
          e.preventDefault();
          const t = text.trim();
          if (!t) {
            setErr("Write a message first.");
            return;
          }
          setErr("");
          setBusy(true);
          try {
            const r = (await api.messages.messageTeams(slug, { team_ids: teamIds, text: t })) as Sent;
            close(false);
            onSent(sentTo(r));
          } catch (x) {
            setErr(errText(x));
          } finally {
            setBusy(false);
          }
        }}
      >
        <div className="flex items-baseline justify-between gap-2">
          <Label htmlFor="tm-text">Message</Label>
          <span className="text-sm text-ink-soft tabular-nums" aria-live="polite">
            {text.length} / {MAX_MESSAGE}
          </span>
        </div>
        <Textarea
          id="tm-text"
          rows={5}
          maxLength={MAX_MESSAGE}
          value={text}
          aria-invalid={!!err || undefined}
          aria-describedby="tm-help tm-err"
          onChange={(e) => setText(e.target.value)}
          autoFocus
        />
        <p id="tm-help" className="mt-1.5 text-sm text-ink-soft">
          Shows on the team’s Overview. Teammates with email updates on also get an email.
        </p>
        <FieldMessage tone="bad" id="tm-err">
          {err}
        </FieldMessage>
        <DialogActions>
          <Button onClick={() => close(false)}>Cancel</Button>
          <Button type="submit" variant="primary" disabled={busy}>
            Send
          </Button>
        </DialogActions>
      </form>
    </Dialog>
  );
}

/** MessageRows is a team's thread: "<sender> (team|organiser) · 3 hours ago · Thu 19 Nov, 4:00 pm". */
export function MessageRows({ messages, event }: { messages: Message[]; event: { time_zone?: string } }) {
  return (
    <ul className="m-0 list-none p-0" aria-label="Messages">
      {messages.map((m, i) => {
        const abs = dateTime(m.created_at, event);
        const rel = agoText(m.created_at);
        return (
          <li key={m.id || i} className="border-b border-rule py-2.5 last:border-b-0">
            <p className="whitespace-pre-wrap">{m.text}</p>
            <p className="mt-0.5 text-sm text-ink-soft">
              {m.sender_name || (m.from_team ? "A teammate" : "An organiser")} ({m.from_team ? "team" : "organiser"}) · {rel ? rel + " · " + abs : abs}
            </p>
          </li>
        );
      })}
    </ul>
  );
}
