// Lives in Simple Hack until it is shared: a kit file feeds the owner app's Tailwind scan and changes its build.
import { useEffect, useMemo, useRef, useState } from "react";
import { Popover as P } from "radix-ui";
import { Button } from "@kit/components/ui/button";

/**
 * A guided walkthrough: a small card beside each step's real target, one step
 * at a time. Non-modal: no dark overlay, the page stays clickable, and
 * following a link elsewhere simply ends it. (The old app's tour.js, on Radix
 * Popover.)
 *
 * A step's target is a CSS selector; `phone` is used instead below 700 px.
 * A step whose target is not on screen is skipped; a step with no target is a
 * card on its own (bottom right of the page; docked at the bottom on a phone).
 * `when` is an extra check for a step whose target is always there but whose
 * words fit only some states. The card counts only the steps that will show.
 */
export type TourStep = { target?: string; phone?: string; when?: () => boolean; title: string; body: string };

const PHONE = "(max-width: 699.98px)";
const isPhone = () => typeof matchMedia === "function" && matchMedia(PHONE).matches;

function visible(el: Element | null): el is HTMLElement {
  if (!el || !el.isConnected) return false;
  const s = getComputedStyle(el);
  if (s.display === "none" || s.visibility === "hidden") return false;
  const r = el.getBoundingClientRect();
  return r.width > 0 && r.height > 0;
}

function targetOf(step: TourStep): HTMLElement | null {
  const sel = isPhone() && step.phone ? step.phone : step.target;
  if (!sel) return null;
  try {
    for (const el of document.querySelectorAll(sel)) if (visible(el)) return el as HTMLElement;
  } catch {
    /* a bad selector shows nothing */
  }
  return null;
}

const canShow = (step: TourStep) => (!step.when || step.when()) && (!step.target || !!targetOf(step));

/** firstShowable is where a tour starts, or -1 when none of its steps can show (then it should not open). */
export function firstShowable(steps: TourStep[]): number {
  return steps.findIndex(canShow);
}

/**
 * Tour shows steps from the first that can show. onDone runs when the person
 * finishes or skips it (record it as seen); onGone when it is torn down
 * without that (a link elsewhere was followed).
 */
export function Tour({ steps, onDone, onGone }: { steps: TourStep[]; onDone: () => void; onGone: () => void }) {
  const [index, setIndex] = useState(() => firstShowable(steps));
  const [, redraw] = useState(0);
  const card = useRef<HTMLDivElement>(null);
  const step = steps[index];
  const target = step ? targetOf(step) : null;

  const shown = steps.map(canShow);
  const total = shown.filter(Boolean).length;
  const number = shown.slice(0, index + 1).filter(Boolean).length;
  const back = shown.slice(0, index).lastIndexOf(true);
  const nextAt = shown.findIndex((ok, i) => ok && i > index);

  // The card's anchor: the target, or a corner of the page for a card on its own.
  const anchor = useMemo(
    () => ({
      current: {
        getBoundingClientRect: () => {
          if (target && target.isConnected && !isPhone()) return target.getBoundingClientRect();
          const w = document.documentElement.clientWidth;
          const h = document.documentElement.clientHeight;
          const bar = parseFloat(getComputedStyle(document.documentElement).getPropertyValue("--sh-bottom-bar")) || 0;
          const x = isPhone() ? w / 2 : w - 24;
          const y = h - bar - 12;
          return DOMRect.fromRect({ x, y, width: 0, height: 0 });
        },
      },
    }),
    [target],
  );

  // Ring the target, and keep it in view.
  useEffect(() => {
    if (!target) return;
    const { outline, outlineOffset } = target.style;
    target.style.outline = "3px solid var(--color-accent)";
    target.style.outlineOffset = "3px";
    try {
      target.scrollIntoView({ block: "nearest" });
    } catch {
      /* old browsers */
    }
    return () => {
      target.style.outline = outline;
      target.style.outlineOffset = outlineOffset;
    };
  }, [target]);

  const next = () => (nextAt === -1 ? onDone() : setIndex(nextAt));
  const prev = () => back !== -1 && setIndex(back);

  // Keys (not while typing in a field), a link followed elsewhere, and a resize that changes what can show.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement | null;
      if (t?.closest?.("input,textarea,select,[contenteditable],[role=radio]")) return;
      if (document.querySelector('[role="dialog"][aria-modal="true"]')) return;
      const act = { Escape: onDone, ArrowRight: next, ArrowLeft: prev }[e.key];
      if (!act) return;
      e.preventDefault();
      act();
    };
    const onClick = (e: MouseEvent) => {
      const el = e.target as HTMLElement | null;
      if (!el || card.current?.contains(el)) return;
      const link = el.closest("a[href]");
      if (!link) return;
      if (target && (target === link || target.contains(link))) onDone();
      else onGone();
    };
    const onResize = () => redraw((n) => n + 1);
    document.addEventListener("keydown", onKey, true);
    document.addEventListener("click", onClick, true);
    window.addEventListener("resize", onResize);
    return () => {
      document.removeEventListener("keydown", onKey, true);
      document.removeEventListener("click", onClick, true);
      window.removeEventListener("resize", onResize);
    };
  });

  if (!step) return null;
  const docked = !target || isPhone();
  return (
    <P.Root open modal={false}>
      <P.Anchor virtualRef={anchor} />
      <P.Portal>
        <P.Content
          ref={card}
          side={docked ? "top" : "bottom"}
          align={docked && !isPhone() ? "end" : "center"}
          sideOffset={12}
          collisionPadding={8}
          avoidCollisions
          aria-labelledby="tour-title"
          onOpenAutoFocus={(e) => {
            e.preventDefault();
            card.current?.focus();
          }}
          onInteractOutside={(e) => e.preventDefault()}
          onEscapeKeyDown={(e) => {
            e.preventDefault();
            onDone();
          }}
          tabIndex={-1}
          className="z-40 w-[min(340px,calc(100vw-16px))] rounded-wobble border-[1.5px] border-ink-soft bg-card px-5 pt-4 pb-4 text-ink shadow-pop outline-none"
        >
          <p className="text-sm text-ink-soft">
            Step {number} of {total}
          </p>
          <h2 id="tour-title" className="mt-1 type-section">
            {step.title}
          </h2>
          <p className="mt-1">{step.body}</p>
          <div className="mt-4 flex flex-wrap items-center gap-2">
            <Button variant="link" className="mr-auto inline-flex min-h-11 min-w-11 items-center justify-center" onClick={onDone}>
              Skip
            </Button>
            <Button onClick={prev} disabled={back === -1}>
              Back
            </Button>
            <Button variant="primary" onClick={next}>
              {nextAt === -1 ? "Done" : "Next"}
            </Button>
          </div>
        </P.Content>
      </P.Portal>
    </P.Root>
  );
}
