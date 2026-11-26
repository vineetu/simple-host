import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { Popover as P } from "radix-ui";
import { Button } from "../components/ui/button";

/**
 * A walkthrough step: a card beside the thing it explains. target is a CSS
 * selector (a step with none sits in the middle of the screen); phone is the
 * selector used below the 700 px breakpoint instead (e.g. the More button for
 * a rail link). when hides a step that does not apply right now. A step whose
 * target is not on screen is skipped.
 */
export type WalkStep = { target?: string; phone?: string; title: string; body: string; when?: () => boolean };

const PHONE = "(max-width: 699.98px)";

function shown(el: Element | null): el is HTMLElement {
  if (!el || !el.isConnected) return false;
  const cs = getComputedStyle(el);
  if (cs.display === "none" || cs.visibility === "hidden") return false;
  const r = el.getBoundingClientRect();
  return r.width > 0 && r.height > 0;
}

function targetOf(step: WalkStep): HTMLElement | null {
  const phone = typeof window !== "undefined" && window.matchMedia(PHONE).matches;
  const sel = phone && step.phone ? step.phone : step.target;
  if (!sel) return null;
  let el: Element | null = null;
  try {
    el = document.querySelector(sel);
  } catch {
    return null;
  }
  return shown(el) ? el : null;
}

const canShow = (s: WalkStep) => (!s.when || s.when()) && (!s.target || !!targetOf(s));

function nextShowable(steps: WalkStep[], from: number, dir: 1 | -1): number {
  for (let i = from; i >= 0 && i < steps.length; i += dir) if (canShow(steps[i])) return i;
  return -1;
}

const RING = ["outline", "outlineOffset", "borderRadius"] as const;

/**
 * Walkthrough is a guided look around a screen, one card at a time (the old
 * tour.js): "Step n of m", Back, Next (Done on the last), Skip. Arrow keys
 * move, Escape skips. The explained thing gets a ring and is scrolled into
 * view. Following a link ends it (finishing it when the link is the step's
 * own target). onFinish runs on Done, Skip and Escape (record "seen" there);
 * onClose runs whenever it goes away.
 */
export function Walkthrough({ steps, open, onFinish, onClose }: { steps: WalkStep[]; open: boolean; onFinish: () => void; onClose: () => void }) {
  const [index, setIndex] = useState(-1);
  const [target, setTarget] = useState<HTMLElement | null>(null);
  const card = useRef<HTMLDivElement>(null);
  const before = useRef<Element | null>(null);

  // Opening: the first step that can show (none: nothing to show, so it just closes).
  useEffect(() => {
    if (!open) return setIndex(-1);
    before.current = document.activeElement;
    const first = nextShowable(steps, 0, 1);
    if (first === -1) onClose();
    else setIndex(first);
  }, [open]); // eslint-disable-line react-hooks/exhaustive-deps

  const step = index >= 0 ? steps[index] : undefined;

  useLayoutEffect(() => {
    if (!step) return setTarget(null);
    const t = step.target ? targetOf(step) : null;
    setTarget(t);
    if (!t) return;
    const old = RING.map((k) => t.style[k]);
    t.style.outline = "3px solid var(--color-accent)";
    t.style.outlineOffset = "3px";
    try {
      t.scrollIntoView({ block: "nearest" });
    } catch {
      /* old browsers */
    }
    return () => RING.forEach((k, i) => (t.style[k] = old[i]));
  }, [step]);

  // Back to wherever focus was before, once it closes.
  useEffect(() => {
    if (open) return;
    const b = before.current as HTMLElement | null;
    before.current = null;
    if (b && b.isConnected && typeof b.focus === "function") b.focus();
  }, [open]);

  const finish = useCallback(() => {
    onFinish();
    onClose();
  }, [onFinish, onClose]);
  const next = useCallback(() => {
    const i = nextShowable(steps, index + 1, 1);
    if (i === -1) finish();
    else setIndex(i);
  }, [steps, index, finish]);
  const back = useCallback(() => {
    const i = nextShowable(steps, index - 1, -1);
    if (i !== -1) setIndex(i);
  }, [steps, index]);

  useEffect(() => {
    if (!step) return;
    const onKey = (e: KeyboardEvent) => {
      const t = e.target as Element | null;
      if (t?.closest?.("input,textarea,select,[contenteditable],[role=radio]")) return;
      if (e.key === "ArrowRight") {
        e.preventDefault();
        next();
      } else if (e.key === "ArrowLeft") {
        e.preventDefault();
        back();
      }
    };
    const onClick = (e: MouseEvent) => {
      const el = e.target as Element | null;
      if (!el || card.current?.contains(el)) return;
      const link = el.closest?.("a[href]");
      if (!link) return;
      if (target && (target === link || target.contains(link))) finish();
      else onClose();
    };
    document.addEventListener("keydown", onKey, true);
    document.addEventListener("click", onClick, true);
    return () => {
      document.removeEventListener("keydown", onKey, true);
      document.removeEventListener("click", onClick, true);
    };
  }, [step, next, back, finish, onClose, target]);

  if (!open || !step) return null;
  const visible = steps.map((s, i) => [s, i] as const).filter(([s]) => canShow(s));
  const at = visible.findIndex(([, i]) => i === index) + 1;
  const last = nextShowable(steps, index + 1, 1) === -1;
  const first = nextShowable(steps, index - 1, -1) === -1;

  return (
    <P.Root open modal={false}>
      {/* Anchored to the target; a step with no target sits in the middle of the screen. */}
      <P.Anchor
        virtualRef={{
          current: {
            getBoundingClientRect: () =>
              target?.getBoundingClientRect() ?? new DOMRect(window.innerWidth / 2, window.innerHeight / 2 - 120, 0, 0),
          },
        }}
      />
      <P.Portal>
        <P.Content
          ref={card}
          key={index}
          role="dialog"
          aria-labelledby="walkthrough-title"
          aria-describedby="walkthrough-body"
          side="bottom"
          align="center"
          sideOffset={12}
          collisionPadding={12}
          avoidCollisions
          onOpenAutoFocus={(e) => {
            e.preventDefault();
            card.current?.focus();
          }}
          onEscapeKeyDown={(e) => {
            e.preventDefault();
            finish();
          }}
          onInteractOutside={(e) => e.preventDefault()}
          tabIndex={-1}
          className="z-50 w-[min(360px,calc(100vw-24px))] rounded-wobble border-[1.5px] border-ink-soft bg-card px-5 pt-4 pb-3 text-ink shadow-pop outline-none"
        >
          <p className="text-sm text-ink-soft">
            Step {at} of {visible.length}
          </p>
          <h2 id="walkthrough-title" className="mt-1 type-section">
            {step.title}
          </h2>
          <p id="walkthrough-body" className="mt-1 text-ink-soft">
            {step.body}
          </p>
          <div className="mt-4 flex flex-wrap items-center gap-2">
            <Button variant="ghost" className="mr-auto" onClick={finish}>
              Skip
            </Button>
            <Button onClick={back} disabled={first}>
              Back
            </Button>
            <Button variant="primary" onClick={next}>
              {last ? "Done" : "Next"}
            </Button>
          </div>
        </P.Content>
      </P.Portal>
    </P.Root>
  );
}
