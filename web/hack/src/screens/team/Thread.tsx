// The team's thread with the organisers (_thread-with-organisers.js): the
// messages both ways, newest first, and the dialog that writes to the
// organisers. Left out on an ended event with nothing in it.
import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Button } from "@kit/components/ui/button";
import { Dialog, DialogActions } from "@kit/components/ui/dialog";
import { FieldMessage, Label, Textarea } from "@kit/components/ui/input";
import { useToast } from "@kit/components/ui/toast";
import { useHack } from "@/lib/client";
import { dateTime } from "@/lib/time";
import { useEventDetail } from "@/app/data";
import type { Message } from "./data";
import { ago, say, signedOut } from "./words";

export const MAX_TEXT = 1000;

export function Thread({ slug, ended, messages }: { slug: string; ended: boolean; messages: Message[] }) {
  const ev = useEventDetail(slug).data?.event;
  const [open, setOpen] = useState(false);
  if (ended && !messages.length) return null;
  return (
    <section className="mt-7 rounded-wobble border border-rule bg-card px-4 py-4 md:px-5" aria-labelledby="thread-h">
      <h2 id="thread-h" className="type-section">
        Messages with the organisers
      </h2>
      {messages.length ? (
        <ul className="m-0 mt-2 list-none p-0" aria-label="Messages">
          {messages.map((m, i) => {
            const rel = ago(m.created_at);
            const abs = dateTime(m.created_at, ev);
            return (
              <li key={m.id || i} className="border-b border-rule py-2.5 last:border-b-0">
                <p className="break-words whitespace-pre-line">{m.text}</p>
                <p className="mt-0.5 text-sm text-ink-soft">
                  {(m.sender_name || (m.from_team ? "A teammate" : "An organiser")) + " (" + (m.from_team ? "team" : "organiser") + ") · " + (rel ? rel + " · " + abs : abs)}
                </p>
              </li>
            );
          })}
        </ul>
      ) : (
        <p className="mt-1 text-ink-soft">No messages yet.</p>
      )}
      {!ended && (
        <div className="mt-3">
          <Button onClick={() => setOpen(true)}>Message the organisers</Button>
        </div>
      )}
      <WriteDialog slug={slug} open={open} onOpenChange={setOpen} />
    </section>
  );
}

function WriteDialog({ slug, open, onOpenChange }: { slug: string; open: boolean; onOpenChange: (o: boolean) => void }) {
  const api = useHack();
  const qc = useQueryClient();
  const toast = useToast();
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
    <Dialog open={open} onOpenChange={close} title="Message the organisers">
      <form
        onSubmit={async (e) => {
          e.preventDefault();
          const t = text.trim();
          if (!t) return setErr("Write a message first.");
          setErr("");
          setBusy(true);
          try {
            await api.messages.messageOrganisers(slug, { text: t });
            close(false);
            toast("Sent to the organisers");
            await qc.invalidateQueries({ queryKey: ["team-thread", slug] });
          } catch (x) {
            if (!signedOut(x)) setErr(say(x, "Could not send the message."));
          } finally {
            setBusy(false);
          }
        }}
      >
        <div className="flex items-baseline justify-between gap-2">
          <Label htmlFor="mo-text">Message</Label>
          <span className="text-sm text-ink-soft" aria-live="polite">
            {text.length} / {MAX_TEXT}
          </span>
        </div>
        <Textarea id="mo-text" rows={5} maxLength={MAX_TEXT} value={text} aria-invalid={!!err || undefined} aria-describedby="mo-msg" onChange={(e) => setText(e.target.value)} autoFocus />
        {err ? (
          <FieldMessage tone="bad" id="mo-msg">
            {err}
          </FieldMessage>
        ) : (
          <p id="mo-msg" className="mt-1.5 text-sm text-ink-soft">
            Every organiser can read it. Your teammates see it too.
          </p>
        )}
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
