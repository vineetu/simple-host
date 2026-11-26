import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { Popover as P } from "radix-ui";
import { Button } from "@kit/components/ui/button";

/**
 * A walkthrough step: a CSS selector for the real thing it points at (the
 * first visible match; `phone` instead below 700 px), or none for a card on
 * its own. `when` is an extra check for a step whose target is always on
 * screen but whose words only fit sometimes. A step whose target is not on
 * screen is skipped, never shown pointing at nothing.
 */
export type TourStep = { target?: string; phone?: string; when?: () => boolean; title: string; body: string };

const PHONE = 700;
const phone = () => window.innerWidth < PHONE;

function visible(el: Element | null): el is HTMLElement {
  if (!el || !(el as HTMLElement).isConnected) return false;
  const cs = getComputedStyle(el);
  if (cs.display === "none" || cs.visibility === "hidden") return false;
  const r = el.getBoundingClientRect();
  return r.width > 0 && r.height > 0;
}

function targetOf(step: TourStep): HTMLElement | null {
  const sel = phone() && step.phone ? step.phone : step.target;
  if (!sel) return null;
  try {
    return [...document.querySelectorAll(sel)].find(visible) || null;
  } catch {
    return null;
  }
}

const canShow = (s: TourStep) => (!s.when || s.when()) && (!s.target || !!targetOf(s));

/** The bottom bar's height (AppShell sets it), so a docked card sits above it. */
function bottomBar(): number {
  const v = getComputedStyle(document.documentElement).getPropertyValue("--sh-bottom-bar");
  return parseFloat(v) || 0;
}

/**
 * (Pass `steps` memoised: a new list starts the walkthrough over.)
 *
 * Walkthrough is a guided look around: a small card beside each step's real
 * target, the target ringed. There is no overlay and the page stays usable;
 * Skip, Done and Escape end it (onDone), and leaving the screen simply
 * unmounts it. On a phone the card docks at the bottom, above the bottom
 * bar, so it never lands over a button mid-page; on a wide screen a step
 * with no target sits at the bottom right.
 */
export function Walkthrough({ steps, open, onDone }: { steps: TourStep[]; open: boolean; onDone: () => void }) {
  const [shown, setShown] = useState<TourStep[] | null>(null);
  const [i, setI] = useState(0);
  const [, setTick] = useState(0);
  const ring = useRef<HTMLElement | null>(null);

  // Which steps can show is read once the screen under it has drawn.
  useEffect(() => {
    if (!open) {
      setShown(null);
      return;
    }
    const id = requestAnimationFrame(() => {
      const list = steps.filter(canShow);
      setI(0);
      // Nothing can show (no target on screen): it does not start, and is not marked seen.
      setShown(list.length ? list : null);
    });
    return () => cancelAnimationFrame(id);
  }, [open, steps]);

  const step = shown?.[i];
  const target = step ? targetOf(step) : null;

  useLayoutEffect(() => {
    const prev = ring.current;
    if (prev) {
      prev.style.outline = "";
      prev.style.outlineOffset = "";
    }
    ring.current = target;
    if (target) {
      target.style.outline = "3px solid var(--color-accent)";
      target.style.outlineOffset = "3px";
      try {
        target.scrollIntoView({ block: "nearest" });
      } catch {
        /* old browsers */
      }
    }
    return () => {
      if (target) {
        target.style.outline = "";
        target.style.outlineOffset = "";
      }
    };
  }, [target]);

  // Re-place on resize (the phone/wide switch changes where the card docks).
  useEffect(() => {
    if (!shown) return;
    const again = () => setTick((t) => t + 1);
    window.addEventListener("resize", again);
    return () => window.removeEventListener("resize", again);
  }, [shown]);

  const docked = !target || phone();
  const anchor = useMemo(
    () => ({
      current: {
        contextElement: target || document.body,
        getBoundingClientRect: () => {
          if (target && !phone()) return target.getBoundingClientRect();
          const vw = document.documentElement.clientWidth;
          const vh = document.documentElement.clientHeight - bottomBar();
          return phone() ? new DOMRect(vw / 2, vh - 12, 0, 0) : new DOMRect(vw - 24, vh - 24, 0, 0);
        },
      },
    }),
    [target],
  );

  if (!shown || !step) return null;
  const last = i === shown.length - 1;
  return (
    <P.Root open modal={false}>
      <P.Anchor virtualRef={anchor as never} />
      <P.Portal>
        <P.Content
          role="dialog"
          aria-modal="false"
          aria-labelledby="tour-title"
          side={docked ? "top" : "bottom"}
          align={docked ? (phone() ? "center" : "end") : "center"}
          sideOffset={12}
          collisionPadding={{ top: 8, left: 8, right: 8, bottom: 8 + bottomBar() }}
          onInteractOutside={(e) => e.preventDefault()}
          onEscapeKeyDown={(e) => {
            e.preventDefault();
            onDone();
          }}
          onKeyDown={(e) => {
            if (e.key === "ArrowRight" && !last) setI(i + 1);
            else if (e.key === "ArrowLeft" && i > 0) setI(i - 1);
          }}
          className="z-50 w-[min(340px,calc(100vw-16px))] rounded-wobble border-[1.5px] border-accent bg-card px-4 pt-3 pb-3 text-ink shadow-pop outline-none"
        >
          <p className="text-xs text-ink-soft">
            Step {i + 1} of {shown.length}
          </p>
          <h2 id="tour-title" className="mt-1 type-section">
            {step.title}
          </h2>
          <p className="mt-1 text-ink-soft">{step.body}</p>
          <div className="mt-3 flex items-center gap-2">
            <Button variant="link" className="mr-auto inline-flex min-h-11 min-w-11 px-1 no-underline" onClick={onDone}>
              Skip
            </Button>
            <Button disabled={i === 0} onClick={() => setI(i - 1)}>
              Back
            </Button>
            <Button variant="primary" onClick={() => (last ? onDone() : setI(i + 1))}>
              {last ? "Done" : "Next"}
            </Button>
          </div>
        </P.Content>
      </P.Portal>
    </P.Root>
  );
}
