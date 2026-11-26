// A guided walkthrough (the old tour.js), on Radix Popover: a small card next
// to each step's real target, no dark overlay, the page still usable under it.
// A step whose target is not on screen is skipped; "Step n of m" counts only
// the steps that show. Skip, Escape or Done ends it and marks it seen.
// Built here for the Team walkthrough; it takes its steps as props, so the
// other walkthroughs can move it to the kit and share it.
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { Popover } from "radix-ui";
import { Button } from "@kit/components/ui/button";

export type Step = { target: string; title: string; body: string };

const visible = (el: Element | null): el is HTMLElement => {
  if (!el || !el.isConnected) return false;
  const r = el.getBoundingClientRect();
  return r.width > 0 && r.height > 0 && getComputedStyle(el).visibility !== "hidden";
};
const find = (sel: string) => {
  try {
    const el = document.querySelector(sel);
    return visible(el) ? el : null;
  } catch {
    return null;
  }
};

export function Walkthrough({ steps, open, onDone }: { steps: Step[]; open: boolean; onDone: () => void }) {
  // Which steps can show is read once the page under it has painted.
  const [shown, setShown] = useState<number[] | null>(null);
  const [at, setAt] = useState(0);
  const anchor = useRef<HTMLElement | null>(null);
  useLayoutEffect(() => {
    if (!open) return setShown(null);
    const ok = steps.map((s, i) => (find(s.target) ? i : -1)).filter((i) => i >= 0);
    setShown(ok);
    setAt(0);
  }, [open, steps]);
  const step = shown && shown.length ? steps[shown[at]] : null;
  const target = useMemo(() => (step ? find(step.target) : null), [step]);
  anchor.current = target;
  useEffect(() => {
    if (open && shown && !shown.length) onDone(); // nothing to point at: do not start
  }, [open, shown, onDone]);
  useEffect(() => {
    if (!target) return;
    // The ring round the target (no stylesheet of its own: the app's is shared).
    const was = [target.style.outline, target.style.outlineOffset, target.style.borderRadius];
    target.style.outline = "3px solid var(--color-accent)";
    target.style.outlineOffset = "4px";
    target.style.borderRadius = target.style.borderRadius || "var(--radius-field)";
    try {
      target.scrollIntoView({ block: "nearest" });
    } catch {
      /* old browsers */
    }
    return () => {
      [target.style.outline, target.style.outlineOffset, target.style.borderRadius] = was;
    };
  }, [target]);
  const count = shown?.length || 0;
  useEffect(() => {
    if (!open || !count) return;
    // Arrow keys step through, except while typing in a field.
    const onKey = (e: KeyboardEvent) => {
      if ((e.target as HTMLElement)?.closest?.("input,textarea,select,[contenteditable],[role=radio]")) return;
      if (e.key === "ArrowRight") setAt((i) => Math.min(i + 1, count - 1));
      else if (e.key === "ArrowLeft") setAt((i) => Math.max(i - 1, 0));
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [open, count]);
  if (!open || !step || !target || !shown) return null;
  const last = at === shown.length - 1;
  return (
    <Popover.Root open onOpenChange={(o) => !o && onDone()}>
      <Popover.Anchor virtualRef={anchor as React.RefObject<HTMLElement>} />
      <Popover.Portal>
        <Popover.Content
          side="bottom"
          align="start"
          sideOffset={10}
          collisionPadding={16}
          aria-label={step.title}
          onInteractOutside={(e) => e.preventDefault()}
          className="z-40 w-[min(340px,calc(100vw-32px))] rounded-wobble border-[1.5px] border-ink-soft bg-card px-4 pt-3 pb-3 text-ink shadow-pop outline-none"
        >
          <p className="text-xs text-ink-soft">
            Step {at + 1} of {shown.length}
          </p>
          <h2 className="mt-1 type-section">{step.title}</h2>
          <p className="mt-1 text-sm text-ink-soft">{step.body}</p>
          <div className="mt-3 flex flex-wrap items-center gap-2">
            <Button variant="link" className="mr-auto min-h-11 min-w-11 px-2" onClick={onDone}>
              Skip
            </Button>
            <Button disabled={at === 0} onClick={() => setAt(at - 1)}>
              Back
            </Button>
            <Button variant="primary" onClick={() => (last ? onDone() : setAt(at + 1))}>
              {last ? "Done" : "Next"}
            </Button>
          </div>
        </Popover.Content>
      </Popover.Portal>
    </Popover.Root>
  );
}

/** seenKey is where a walkthrough with no server preference keeps "seen", per person (tour.js localSeenKey). */
export const seenKey = (base: string, uid: string) => base + "-" + (uid || "anon");
export function wasSeen(key: string): boolean {
  try {
    return localStorage.getItem(key) === "1";
  } catch {
    return false;
  }
}
export function markSeen(key: string) {
  try {
    localStorage.setItem(key, "1");
  } catch {
    /* storage blocked */
  }
}
