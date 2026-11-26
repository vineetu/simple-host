// The one unsaved-changes guard (the old ui.js trackForm / confirmFormLeave).
// A form says it has edits with useUnsaved(true); the app renders
// <UnsavedGuard /> once, inside its data router. While anything has edits:
// - a click on a same-origin link (side rail, bottom bar, tabs, a link in the
//   page, Sign out) waits for "Leave without saving?": Stay or Leave;
// - any other move inside the app (Back and Forward, a tab or button that
//   navigates, a redirect) is held by the router's blocker for the same question;
// - closing or reloading the tab, or a full page load elsewhere, gets the
//   browser's own question.
// Code that moves on by itself after a save calls clearUnsaved() first.
// (Outside kit/components/ui on purpose: Simple Host's Tailwind scans that folder.)
import { useEffect, useState } from "react";
import { useBlocker } from "react-router";
import { Button } from "../components/ui/button";
import { Dialog, DialogActions } from "../components/ui/dialog";

const dirty = new Set<object>();
let replaying = false;
let ask: ((resume: () => void) => void) | null = null;

/** useUnsaved marks this part of the screen as having unsaved edits while on is true. */
export function useUnsaved(on: boolean) {
  useEffect(() => {
    if (!on) return;
    const token = {};
    dirty.add(token);
    return () => void dirty.delete(token);
  }, [on]);
}

/** hasUnsaved is whether any part of the screen has unsaved edits. */
export const hasUnsaved = () => dirty.size > 0;

/** clearUnsaved forgets every edit (after a save that moves on by itself, or a Discard). */
export function clearUnsaved() {
  dirty.clear();
}

/**
 * confirmLeave asks the question for a move the app makes itself (a button
 * that signs out, a tab kept in component state): go runs at once when
 * nothing is unsaved, else after Leave.
 */
export function confirmLeave(go: () => void) {
  if (!dirty.size || !ask) return go();
  ask(() => {
    dirty.clear();
    go();
  });
}

function onClick(e: MouseEvent) {
  if (replaying || !dirty.size || !ask) return;
  const a = (e.target as Element | null)?.closest?.("a[href]") as HTMLAnchorElement | null;
  if (!a || e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey || a.target || a.hasAttribute("download")) return;
  if (a.closest("[aria-disabled='true'],[data-unsaved-ok]")) return;
  const url = new URL(a.href, location.href);
  if (!/^https?:$/.test(url.protocol) || url.origin !== location.origin) return;
  if (url.pathname === location.pathname && url.search === location.search) return; // a #hash on this page
  e.preventDefault();
  e.stopPropagation();
  ask(() => {
    dirty.clear();
    replaying = true;
    try {
      a.click();
    } finally {
      replaying = false;
    }
  });
}

function onUnload(e: BeforeUnloadEvent) {
  if (!dirty.size) return;
  e.preventDefault();
  e.returnValue = "";
}

/** UnsavedGuard is the question itself; render it once, inside the app's data router. */
export function UnsavedGuard() {
  const [resume, setResume] = useState<(() => void) | null>(null);
  const blocker = useBlocker(({ currentLocation: a, nextLocation: b }) => dirty.size > 0 && (a.pathname !== b.pathname || a.search !== b.search));
  useEffect(() => {
    ask = (r) => setResume(() => r);
    document.addEventListener("click", onClick, true);
    window.addEventListener("beforeunload", onUnload);
    return () => {
      ask = null;
      document.removeEventListener("click", onClick, true);
      window.removeEventListener("beforeunload", onUnload);
    };
  }, []);
  const blocked = blocker.state === "blocked";
  const stay = () => {
    setResume(null);
    if (blocked) blocker.reset();
  };
  const leave = () => {
    const r = resume;
    setResume(null);
    dirty.clear();
    if (blocked) blocker.proceed();
    else r?.();
  };
  return (
    <Dialog open={!!resume || blocked} onOpenChange={(o) => !o && stay()} title="Leave without saving?" description="Your changes on this page are not saved yet.">
      <DialogActions>
        <Button onClick={stay}>Stay</Button>
        <Button variant="danger" onClick={leave}>
          Leave
        </Button>
      </DialogActions>
    </Dialog>
  );
}
