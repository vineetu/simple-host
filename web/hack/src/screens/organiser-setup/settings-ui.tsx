// Pieces only Settings uses: a section's Save row, the typed confirm (Delete
// event), the lock rules per tab, and scrolling to an old #anchor.
import { useEffect, useState, type FormEvent, type ReactNode } from "react";
import { useLocation } from "react-router";
import { Button } from "@kit/components/ui/button";
import { Dialog, DialogActions } from "@kit/components/ui/dialog";
import { FieldMessage, Input, Label } from "@kit/components/ui/input";
import { Skeleton } from "@kit/components/ui/skeleton";
import type { EventDetail } from "@/lib/client";
import { writeLock } from "@/shared/locks";

/** Locks is what can still change on Settings: most of it stops when the event ends; details and the directory listing do not. */
export function locks(d: EventDetail) {
  const admin = d.admin_view ? "Viewing as platform admin. Everything here is read only." : d.event.taken_down ? "This event has been taken down. Nothing on this page can be changed." : "";
  return { settings: writeLock(d, { settings: true }), admin, ended: d.event.stage === "archived" };
}

/** SaveRow is one section's save: its error under the fields, then the button (filled only when it is the tab's one form). */
export function SaveRow({
  busy,
  disabled,
  label = "Save",
  error,
  primary,
  children,
  note,
}: {
  busy?: boolean;
  disabled?: boolean;
  label?: string;
  error?: string;
  primary?: boolean;
  children?: ReactNode;
  note?: ReactNode;
}) {
  return (
    <div className="mt-3">
      {error && <FieldMessage tone="bad">{error}</FieldMessage>}
      <div className="mt-2 flex flex-wrap items-center gap-2.5">
        <Button type="submit" variant={primary ? "primary" : "secondary"} disabled={disabled || busy}>
          {busy ? "Saving…" : label}
        </Button>
        {children}
        {note && <span className="text-sm text-ink-soft">{note}</span>}
      </div>
    </div>
  );
}

/** submit wraps a form's submit handler (no page load). */
export const submit = (fn: () => void) => (e: FormEvent) => {
  e.preventDefault();
  fn();
};

/** TabLoading holds the tab's place while its settings load. */
export function TabLoading() {
  return (
    <div role="status" aria-busy="true" aria-label="Loading">
      <Skeleton className="mt-5 h-40 w-full max-w-prose" />
      <Skeleton className="mt-4 h-28 w-full max-w-prose" />
    </div>
  );
}

/** useAnchor scrolls to the section an old #link names once the tab has drawn it. */
export function useAnchor(ready: boolean) {
  const { hash } = useLocation();
  useEffect(() => {
    if (!ready || !hash) return;
    let id = "";
    try {
      id = decodeURIComponent(hash.slice(1));
    } catch {
      return;
    }
    document.getElementById(id)?.scrollIntoView?.();
  }, [ready, hash]);
}

/** TypedConfirm asks for an action that cannot be undone by having the exact word typed (the event's address name). */
export function TypedConfirm({
  open,
  onOpenChange,
  title,
  body,
  match,
  confirmLabel,
  onConfirm,
}: {
  open: boolean;
  onOpenChange: (o: boolean) => void;
  title: string;
  body: ReactNode;
  /** The text to type; empty asks with no field. */
  match: string;
  confirmLabel: string;
  onConfirm: () => Promise<unknown>;
}) {
  const [typed, setTyped] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const close = (o: boolean) => {
    if (!o) {
      setTyped("");
      setError("");
    }
    onOpenChange(o);
  };
  const ok = !match || typed.trim() === match;
  return (
    <Dialog open={open} onOpenChange={close} title={title} description={body}>
      <form
        onSubmit={async (e) => {
          e.preventDefault();
          if (!ok) return;
          setBusy(true);
          setError("");
          try {
            await onConfirm();
            close(false);
          } catch (err) {
            setError((err as Error).message || "Something went wrong.");
          } finally {
            setBusy(false);
          }
        }}
      >
        {match && (
          <>
            <Label htmlFor="typed-confirm">
              Type <span className="font-mono text-ink">{match}</span> to confirm
            </Label>
            <Input id="typed-confirm" autoComplete="off" spellCheck={false} value={typed} onChange={(e) => setTyped(e.target.value)} className="font-mono" />
          </>
        )}
        {error && <FieldMessage tone="bad">{error}</FieldMessage>}
        <DialogActions>
          <Button onClick={() => close(false)}>Cancel</Button>
          <Button type="submit" variant="danger" disabled={!ok || busy}>
            {confirmLabel}
          </Button>
        </DialogActions>
      </form>
    </Dialog>
  );
}
