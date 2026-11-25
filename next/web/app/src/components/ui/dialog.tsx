import { useState, type ReactNode } from "react";
import { Dialog as D } from "radix-ui";
import { cn } from "@/lib/utils";
import { Button } from "./button";
import { Input, Label } from "./input";

/** Dialog is a modal paper card over the page (Radix: focus trap, Escape, labelled). */
export function Dialog({
  open,
  onOpenChange,
  title,
  description,
  children,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  description?: ReactNode;
  children: ReactNode;
}) {
  return (
    <D.Root open={open} onOpenChange={onOpenChange}>
      <D.Portal>
        <D.Overlay className="fixed inset-0 z-40 bg-ink/40" />
        <D.Content
          className={cn(
            "fixed top-1/2 left-1/2 z-50 max-h-[calc(100dvh-32px)] w-[min(440px,calc(100vw-32px))] -translate-x-1/2 -translate-y-1/2 overflow-auto",
            "rounded-wobble border-[1.5px] border-ink-soft bg-card px-6 pt-6 pb-5 text-ink shadow-pop",
          )}
        >
          <D.Title className="mb-2 font-hand text-2xl font-bold">{title}</D.Title>
          {description ? (
            <D.Description className="mb-4 text-ink-soft">{description}</D.Description>
          ) : (
            <D.Description className="sr-only">{title}</D.Description>
          )}
          {children}
        </D.Content>
      </D.Portal>
    </D.Root>
  );
}

export function DialogActions({ children }: { children: ReactNode }) {
  return <div className="mt-5 flex flex-wrap justify-end gap-2.5">{children}</div>;
}

/**
 * AskDialog asks to confirm one action, optionally with a one-line answer
 * (a reason). onConfirm gets the answer; it may return a promise while it works.
 */
export function AskDialog({
  open,
  onOpenChange,
  title,
  body,
  label,
  confirmLabel,
  danger,
  onConfirm,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  body?: string;
  label?: string;
  confirmLabel: string;
  danger?: boolean;
  onConfirm: (answer: string) => void | Promise<unknown>;
}) {
  const [answer, setAnswer] = useState("");
  const [busy, setBusy] = useState(false);
  const close = (o: boolean) => {
    if (!o) setAnswer("");
    onOpenChange(o);
  };
  return (
    <Dialog open={open} onOpenChange={close} title={title} description={body}>
      <form
        onSubmit={async (e) => {
          e.preventDefault();
          if (label && !answer.trim()) return;
          setBusy(true);
          try {
            await onConfirm(answer.trim());
            close(false);
          } finally {
            setBusy(false);
          }
        }}
      >
        {label && (
          <>
            <Label htmlFor="ask-answer">{label}</Label>
            <Input id="ask-answer" autoComplete="off" required value={answer} onChange={(e) => setAnswer(e.target.value)} autoFocus />
          </>
        )}
        <DialogActions>
          <Button onClick={() => close(false)}>Cancel</Button>
          <Button type="submit" variant={danger ? "danger" : "primary"} className={danger ? "" : "px-6"} disabled={busy}>
            {confirmLabel}
          </Button>
        </DialogActions>
      </form>
    </Dialog>
  );
}
