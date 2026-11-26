// Announcements (/e/{slug}/manage?s=announcements): post an update to
// everyone (optionally by email, the button saying how many), preview it as
// it will show, and pin, edit or delete what was posted. Pin has Undo;
// Delete asks, since the post already reached people's feeds and inboxes.
// Ended events and a platform admin's look are read only.
import { useEffect, useId, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Badge } from "@kit/components/ui/badge";
import { Button } from "@kit/components/ui/button";
import { Check } from "@kit/components/ui/choice";
import { AskDialog, Dialog, DialogActions } from "@kit/components/ui/dialog";
import { EmptyState } from "@kit/components/ui/empty-state";
import { Field, FieldMessage, Input, Label, Textarea } from "@kit/components/ui/input";
import { Menu } from "@kit/components/ui/menu";
import { Notice } from "@kit/components/ui/notice";
import { PageTitle, Section } from "@kit/components/ui/page-shell";
import { Skeleton } from "@kit/components/ui/skeleton";
import { useToast } from "@kit/components/ui/toast";
import { useHack } from "@/lib/client";
import { dateTime } from "@/lib/time";
import { useEventDetail } from "@/app/data";
import type { ScreenProps } from "@/routes/registry";
import { errText, useRefreshEvent } from "./common";
import { linkFor, postedText, summary, type Announcement, type Posted } from "./data-announcements";
import { useUnsaved } from "@kit/guard/unsaved";

type Draft = { title: string; body: string };

/** AddLink asks for a label and an address and hands back the text to insert. */
function AddLink({ open, onOpenChange, onAdd }: { open: boolean; onOpenChange: (o: boolean) => void; onAdd: (text: string) => void }) {
  const [label, setLabel] = useState("");
  const [address, setAddress] = useState("");
  const [error, setError] = useState("");
  const id = useId();
  const close = (o: boolean) => {
    if (!o) {
      setLabel("");
      setAddress("");
      setError("");
    }
    onOpenChange(o);
  };
  return (
    <Dialog open={open} onOpenChange={close} title="Add link">
      <form
        onSubmit={(e) => {
          e.preventDefault();
          e.stopPropagation();
          const r = linkFor(address, label);
          if ("error" in r) return setError(r.error);
          onAdd(r.text);
          close(false);
        }}
      >
        <Field id={id + "-label"} label="Label">
          <Input id={id + "-label"} value={label} onChange={(e) => setLabel(e.target.value)} autoFocus />
        </Field>
        <Field id={id + "-address"} label="Address" error={error}>
          <Input id={id + "-address"} inputMode="url" value={address} aria-invalid={!!error} onChange={(e) => setAddress(e.target.value)} />
        </Field>
        <DialogActions>
          <Button onClick={() => close(false)}>Cancel</Button>
          <Button type="submit" variant="primary">
            Add link
          </Button>
        </DialogActions>
      </form>
    </Dialog>
  );
}

/** Composer is the title, the message (with Add link) and the preview the server renders, for posting and for editing. */
function Composer({ slug, draft, set, errors, counter }: { slug: string; draft: Draft; set: (d: Draft) => void; errors: Partial<Draft>; counter?: boolean }) {
  const api = useHack();
  const id = useId();
  const area = useRef<HTMLTextAreaElement>(null);
  const [linking, setLinking] = useState(false);
  const [preview, setPreview] = useState<{ html?: string; error?: string } | null>(null);
  const range = useRef<[number, number]>([0, 0]);
  useEffect(() => {
    if (!draft.body.trim()) return setPreview(null);
    let live = true;
    const t = setTimeout(async () => {
      try {
        const r = (await api.messages.preview(slug, { body: draft.body })) as { body_html?: string };
        if (live) setPreview({ html: r.body_html || "" });
      } catch (e) {
        if (live) setPreview({ error: errText(e) });
      }
    }, 300);
    return () => {
      live = false;
      clearTimeout(t);
    };
  }, [api, slug, draft.body]);
  return (
    <>
      <div className="mb-4">
        <div className="flex items-end justify-between gap-2">
          <Label htmlFor={id + "-title"}>Title</Label>
          {counter && (
            <span className="mb-1.5 text-sm text-ink-soft" aria-live="polite">
              {draft.title.length} / 120
            </span>
          )}
        </div>
        <Input id={id + "-title"} maxLength={120} value={draft.title} aria-invalid={!!errors.title} onChange={(e) => set({ ...draft, title: e.target.value })} />
        {errors.title && <FieldMessage tone="bad">{errors.title}</FieldMessage>}
      </div>
      <div className="mb-4">
        <div className="mb-1.5 flex items-center justify-between gap-2">
          <Label htmlFor={id + "-body"} className="mb-0">
            Message
          </Label>
          <Button
            variant="ghost"
            onClick={() => {
              const el = area.current;
              range.current = el ? [el.selectionStart, el.selectionEnd] : [draft.body.length, draft.body.length];
              setLinking(true);
            }}
          >
            Add link
          </Button>
        </div>
        <Textarea ref={area} id={id + "-body"} rows={4} maxLength={3000} value={draft.body} aria-invalid={!!errors.body} onChange={(e) => set({ ...draft, body: e.target.value })} />
        {errors.body && <FieldMessage tone="bad">{errors.body}</FieldMessage>}
      </div>
      {preview && (
        <section aria-label="Preview" className="mb-4 rounded-field border border-dashed border-ink-soft bg-paper px-4 py-3">
          <p className="type-label text-ink-soft">Preview</p>
          <h3 className="mt-1 type-section">{draft.title}</h3>
          {preview.error ? (
            <p className="mt-1 text-bad">{preview.error}</p>
          ) : (
            // The server renders the body (links, bold) and escapes everything else.
            <div className="mt-1 [&_p]:my-1" dangerouslySetInnerHTML={{ __html: preview.html || "" }} />
          )}
        </section>
      )}
      <AddLink
        open={linking}
        onOpenChange={setLinking}
        onAdd={(text) => {
          const [a, b] = range.current;
          if (draft.body.length - (b - a) + text.length > 3000) return;
          set({ ...draft, body: draft.body.slice(0, a) + text + draft.body.slice(b) });
          setTimeout(() => area.current?.focus());
        }}
      />
    </>
  );
}

const check = (d: Draft): Partial<Draft> => (!d.title.trim() ? { title: "Title is required." } : !d.body.trim() ? { body: "Message is required." } : {});

function NewAnnouncement({ slug, emailable, onPosted }: { slug: string; emailable: number; onPosted: () => Promise<unknown> }) {
  const api = useHack();
  const toast = useToast();
  const [draft, setDraft] = useState<Draft>({ title: "", body: "" });
  const [email, setEmail] = useState(false);
  const [errors, setErrors] = useState<Partial<Draft>>({});
  const [busy, setBusy] = useState(false);
  const dirty = !!(draft.title || draft.body);
  // Leaving with unsaved lines asks first.
  useUnsaved(dirty);
  return (
    <form
      aria-labelledby="ann-h"
      className="rounded-wobble border border-rule bg-card px-4 py-4 md:px-5"
      noValidate
      onSubmit={async (e) => {
        e.preventDefault();
        const bad = check(draft);
        setErrors(bad);
        if (bad.title || bad.body) return;
        setBusy(true);
        try {
          const r = (await api.messages.post(slug, { title: draft.title, body: draft.body, email_participants: email })) as Posted;
          const line = postedText(email, r);
          setDraft({ title: "", body: "" });
          setEmail(false);
          await onPosted();
          toast(line.text, { bad: line.bad });
        } catch (err) {
          toast(errText(err), { bad: true });
        } finally {
          setBusy(false);
        }
      }}
    >
      <h2 id="ann-h" className="mb-3 type-section">
        New announcement
      </h2>
      <Composer slug={slug} draft={draft} set={setDraft} errors={errors} counter />
      <Check checked={email} onChange={setEmail} disabled={!emailable} hint={emailable ? "Editing it later never sends another email." : "No participant has an email address yet."}>
        Email {emailable} participants
      </Check>
      <div className="mt-3 flex flex-wrap items-center justify-between gap-3">
        <span className="text-sm text-ink-soft">Shows on your page and in the app straight away.</span>
        <Button type="submit" variant="primary" disabled={busy}>
          {email ? `Post and email ${emailable} participants` : "Post"}
        </Button>
      </div>
    </form>
  );
}

function EditDialog({ slug, a, onClose, onSaved }: { slug: string; a: Announcement | null; onClose: () => void; onSaved: () => Promise<unknown> }) {
  const api = useHack();
  const [draft, setDraft] = useState<Draft>({ title: "", body: "" });
  const [errors, setErrors] = useState<Partial<Draft>>({});
  const [failed, setFailed] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    if (a) {
      setDraft({ title: a.title, body: a.body });
      setErrors({});
      setFailed("");
    }
  }, [a]);
  return (
    <Dialog open={!!a} onOpenChange={(o) => !o && onClose()} title="Edit announcement" description="Saving changes doesn't email anyone again.">
      <form
        noValidate
        onSubmit={async (e) => {
          e.preventDefault();
          const bad = check(draft);
          setErrors(bad);
          setFailed("");
          if (bad.title || bad.body) return;
          setBusy(true);
          try {
            await api.messages.edit(slug, a!.id, { title: draft.title.trim(), body: draft.body.trim() });
            await onSaved();
            onClose();
          } catch (err) {
            setFailed(errText(err));
          } finally {
            setBusy(false);
          }
        }}
      >
        <Composer slug={slug} draft={draft} set={setDraft} errors={errors} />
        {failed && <FieldMessage tone="bad">{failed}</FieldMessage>}
        <DialogActions>
          <Button onClick={onClose}>Cancel</Button>
          <Button type="submit" variant="primary" disabled={busy}>
            Save
          </Button>
        </DialogActions>
      </form>
    </Dialog>
  );
}

function Row({ a, tz, readOnly, onEdit, onPin, onDelete }: { a: Announcement; tz?: string; readOnly: boolean; onEdit: () => void; onPin: () => void; onDelete: () => void }) {
  const meta = (
    <span className="mt-1 flex flex-wrap items-center gap-1.5 text-sm text-ink-soft">
      <span>{dateTime(a.created_at, tz)}</span>
      {a.pinned && <Badge>Pinned</Badge>}
      {(a.emails_sent || 0) > 0 && (
        <Badge tone="ok">
          Emailed {a.emails_sent} {a.emails_sent === 1 ? "person" : "people"}
        </Badge>
      )}
    </span>
  );
  const text = (
    <>
      <span className="block font-semibold text-ink">{a.title}</span>
      <span className="line-clamp-2 block text-sm text-ink-soft">{summary(a.body)}</span>
      {meta}
    </>
  );
  return (
    <li className="flex items-start gap-2 border-b border-rule px-1 py-2.5 last:border-b-0">
      {readOnly ? (
        <span className="min-w-0 flex-1">{text}</span>
      ) : (
        <>
          <button type="button" className="min-h-11 min-w-0 flex-1 cursor-pointer text-left" onClick={onEdit}>
            {text}
          </button>
          <Button className="hidden md:inline-flex" onClick={onEdit} aria-label={"Edit " + a.title}>
            Edit
          </Button>
          <Menu
            label={"More for " + a.title}
            items={[{ label: a.pinned ? "Unpin" : "Pin to top", onSelect: onPin }, { label: "Edit", onSelect: onEdit }, { separator: true }, { label: "Delete", danger: true, onSelect: onDelete }]}
          />
        </>
      )}
    </li>
  );
}

export default function Announcements({ match }: ScreenProps) {
  const slug = match.slug!;
  const api = useHack();
  const qc = useQueryClient();
  const toast = useToast();
  const refreshEvent = useRefreshEvent(slug);
  const d = useEventDetail(slug).data;
  const ev = d?.event;
  const admin = !!d?.admin_view;
  const ended = ev?.stage === "archived";
  const readOnly = admin || ended || !!ev?.taken_down;
  const emailable = (d?.organiser?.counts as Record<string, number> | undefined)?.emailable_participants || 0;
  const q = useQuery({ queryKey: ["announcements", slug], queryFn: async () => ((await api.messages.announcements(slug)) as { announcements?: Announcement[] }).announcements || [] });
  const [editing, setEditing] = useState<Announcement | null>(null);
  const [deleting, setDeleting] = useState<Announcement | null>(null);
  const reload = () => Promise.all([qc.invalidateQueries({ queryKey: ["announcements", slug] }), refreshEvent()]);

  const pin = async (a: Announcement, pinned: boolean, undo = true) => {
    try {
      await api.messages.edit(slug, a.id, { pinned });
    } catch (e) {
      toast(errText(e), { bad: true });
      return;
    }
    await reload();
    toast(pinned ? "Pinned " + a.title : "Unpinned " + a.title, undo ? { undo: { label: "Undo", run: () => void pin(a, !pinned, false) } } : undefined);
  };

  const title = <PageTitle sub="Updates for everyone in the event, on your public page and in participants' app.">Announcements</PageTitle>;
  let body;
  if (q.isPending)
    body = (
      <div className="mt-7" role="status" aria-busy="true" aria-label="Loading announcements">
        {!readOnly && <Skeleton className="h-64 w-full" />}
        <Skeleton className="mt-6 h-16 w-full" />
        <Skeleton className="mt-2 h-16 w-full" />
      </div>
    );
  else {
    const list = q.data || [];
    body = (
      <>
        {admin && <Notice tone="bad" title="Viewing as platform admin. Everything here is read only." />}
        {!admin && ended && <Notice title="This event has ended. The list below stays readable; nothing can be posted or changed." />}
        {!admin && !ended && ev?.taken_down && <Notice title="This event has been taken down. Nothing on this page can be changed." />}
        {q.isError && <Notice tone="bad" title="Could not load announcements." action={<Button onClick={() => q.refetch()}>Try again</Button>} />}
        {!readOnly && (
          <div className="mt-6">
            <NewAnnouncement slug={slug} emailable={emailable} onPosted={reload} />
          </div>
        )}
        <Section title="Posted" count={list.length || undefined}>
          {list.length ? (
            <ul aria-label="Posted announcements" className="m-0 list-none rounded-wobble border border-rule bg-card px-3 py-1">
              {list.map((a) => (
                <Row key={a.id} a={a} tz={ev?.time_zone} readOnly={readOnly} onEdit={() => setEditing(a)} onPin={() => void pin(a, !a.pinned)} onDelete={() => setDeleting(a)} />
              ))}
            </ul>
          ) : (
            !q.isError && <EmptyState title="Nothing posted yet">Your first announcement shows here, on your page and in participants' app.</EmptyState>
          )}
        </Section>
      </>
    );
  }

  return (
    <>
      <div className="md:max-w-prose">
        {title}
        {body}
      </div>
      <EditDialog slug={slug} a={editing} onClose={() => setEditing(null)} onSaved={reload} />
      <AskDialog
        open={!!deleting}
        onOpenChange={(o) => !o && setDeleting(null)}
        title={`Delete “${deleting?.title || ""}”?`}
        body="It disappears from your page and from everyone's app. Emails already sent stay sent."
        confirmLabel="Delete announcement"
        danger
        onConfirm={async () => {
          try {
            await api.messages.removeAnnouncement(slug, deleting!.id);
          } catch (e) {
            toast(errText(e), { bad: true });
            return;
          }
          await reload();
          toast("Deleted");
        }}
      />
    </>
  );
}
