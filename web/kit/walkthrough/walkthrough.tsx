import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { Popover as P } from "radix-ui";
import { Button } from "../components/ui/button";

// The one walkthrough engine (the old tour.js, on Radix Popover). Every guided
// look around in Simple Hack uses it: organiser Home, participant Overview,
// Team, judge Overview and Scoring. It lives outside kit/components/ui on
// purpose: Simple Host's Tailwind scans that folder, so a file there would
// change Host's built CSS.

/**
 * A walkthrough step: a card beside the thing it explains. target is a CSS
 * selector (the first visible match; a step with none is a card on its own);
 * phone is the selector used below the 700 px breakpoint instead (e.g. the
 * More button for a rail link). when hides a step whose words fit only some
 * states. A step whose target is not on screen is skipped, never shown
 * pointing at nothing.
 */
export type WalkStep = { target?: string; phone?: string; title: string; body: string; when?: () => boolean };

const PHONE = "(max-width: 699.98px)";
const isPhone = () => typeof matchMedia === "function" && matchMedia(PHONE).matches;

function shown(el: Element | null): el is HTMLElement {
  if (!el || !el.isConnected) return false;
  const cs = getComputedStyle(el);
  if (cs.display === "none" || cs.visibility === "hidden") return false;
  const r = el.getBoundingClientRect();
  return r.width > 0 && r.height > 0;
}

function targetOf(step: WalkStep): HTMLElement | null {
  const sel = isPhone() && step.phone ? step.phone : step.target;
  if (!sel) return null;
  try {
    for (const el of document.querySelectorAll(sel)) if (shown(el)) return el as HTMLElement;
  } catch {
    /* a bad selector shows nothing */
  }
  return null;
}

const canShow = (s: WalkStep) => (!s.when || s.when()) && (!s.target || !!targetOf(s));

function nextShowable(steps: WalkStep[], from: number, dir: 1 | -1): number {
  for (let i = from; i >= 0 && i < steps.length; i += dir) if (canShow(steps[i])) return i;
  return -1;
}

/** firstShowable is where a walkthrough starts, or -1 when none of its steps can show (then it should not open). */
export const firstShowable = (steps: WalkStep[]) => nextShowable(steps, 0, 1);

/** The bottom bar's height (the app shell sets it), so a docked card sits above it. */
const bottomBar = () => parseFloat(getComputedStyle(document.documentElement).getPropertyValue("--sh-bottom-bar")) || 0;

const RING = ["outline", "outlineOffset"] as const;

/**
 * Walkthrough is a guided look around a screen, one card at a time: "Step n
 * of m" (counting only the steps that show), Back, Next (Done on the last),
 * Skip. Arrow keys move, Escape skips. No overlay: the page stays usable. The
 * explained thing gets a ring and is scrolled into view. On a phone, and for
 * a step with no target, the card docks at the bottom (above the bottom bar
 * on a phone, bottom right on a wide screen), and the page gets room below
 * its last section so the card never covers it. Following a link ends it
 * (finishing it when the link is the step's own target). onFinish runs on
 * Done, Skip and Escape (record "seen" there); onClose runs whenever it goes
 * away. When nothing can show it closes without onFinish.
 */
export function Walkthrough({ steps, open, onFinish, onClose }: { steps: WalkStep[]; open: boolean; onFinish: () => void; onClose: () => void }) {
  const [index, setIndex] = useState(-1);
  const [target, setTarget] = useState<HTMLElement | null>(null);
  const [, redraw] = useState(0);
  const card = useRef<HTMLDivElement>(null);
  const before = useRef<Element | null>(null);

  // Opening: after the frame (the screen under it has drawn), the first step that can show.
  useEffect(() => {
    if (!open) return setIndex(-1);
    before.current = document.activeElement;
    const id = requestAnimationFrame(() => {
      const first = firstShowable(steps);
      if (first === -1) onClose();
      else setIndex(first);
    });
    return () => cancelAnimationFrame(id);
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

  const docked = !!step && (!target || isPhone());

  // Room below the page's last section while a docked card is up, so it covers nothing.
  useLayoutEffect(() => {
    const el = card.current;
    if (!open || !docked || !el || typeof ResizeObserver === "undefined") return;
    const body = document.body;
    const was = body.style.paddingBottom;
    const fit = () => void (body.style.paddingBottom = el.getBoundingClientRect().height + 24 + "px");
    fit();
    const ro = new ResizeObserver(fit);
    ro.observe(el);
    return () => {
      ro.disconnect();
      body.style.paddingBottom = was;
    };
  }, [open, docked, index]);

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
      if (document.querySelector('[role="dialog"][aria-modal="true"]')) return;
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
    // The phone/wide switch changes where the card sits.
    const onResize = () => redraw((n) => n + 1);
    document.addEventListener("keydown", onKey, true);
    document.addEventListener("click", onClick, true);
    window.addEventListener("resize", onResize);
    return () => {
      document.removeEventListener("keydown", onKey, true);
      document.removeEventListener("click", onClick, true);
      window.removeEventListener("resize", onResize);
    };
  }, [step, next, back, finish, onClose, target]);

  const anchor = useMemo(
    () => ({
      current: {
        getBoundingClientRect: () => {
          if (target && target.isConnected && !isPhone()) return target.getBoundingClientRect();
          const w = document.documentElement.clientWidth;
          const h = document.documentElement.clientHeight - bottomBar();
          return isPhone() ? new DOMRect(w / 2, h - 12, 0, 0) : new DOMRect(w - 24, h - 24, 0, 0);
        },
      },
    }),
    [target],
  );

  if (!open || !step) return null;
  const total = steps.filter(canShow).length;
  const at = steps.slice(0, index + 1).filter(canShow).length;
  const last = nextShowable(steps, index + 1, 1) === -1;
  const first = nextShowable(steps, index - 1, -1) === -1;

  return (
    <P.Root open modal={false}>
      <P.Anchor virtualRef={anchor} />
      <P.Portal>
        <P.Content
          ref={card}
          key={index}
          role="dialog"
          aria-modal="false"
          aria-labelledby="walkthrough-title"
          aria-describedby="walkthrough-body"
          side={docked ? "top" : "bottom"}
          align={docked && !isPhone() ? "end" : "center"}
          sideOffset={12}
          collisionPadding={{ top: 8, left: 8, right: 8, bottom: 8 + bottomBar() }}
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
          className="z-50 w-[min(360px,calc(100vw-16px))] rounded-wobble border-[1.5px] border-ink-soft bg-card px-5 pt-4 pb-3 text-ink shadow-pop outline-none"
        >
          <p className="text-sm text-ink-soft">
            Step {at} of {total}
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

/** Seen-state kept in this browser, for walkthroughs the server has no preference for (tour.js localSeenKey). */
export function seenHere(key: string): boolean {
  try {
    return localStorage.getItem(key) === "1";
  } catch {
    return false;
  }
}
export function markSeenHere(key: string) {
  try {
    localStorage.setItem(key, "1");
  } catch {
    /* storage blocked */
  }
}
