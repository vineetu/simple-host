// "Leave without saving?": a form with edits asks before the person goes
// (Simple Hack's old ui.js trackForm / confirmFormLeave). A part of a screen
// says it has edits with useDirty(true); the screen renders <LeaveGuard />
// once. While anything has edits, a click on any same-origin link (the rail,
// the bottom bar, a link in the page, Sign out) waits for Stay or Leave, and
// closing or reloading the tab gets the browser's own question.
// Code that moves on by itself after a save calls clearDirty() first.
// (Back and Forward are not guarded: the router has already moved by the time
// the browser says so; the old app's restore needs a data router.)
import { useEffect, useState } from "react";
import { Button } from "../components/ui/button";
import { Dialog, DialogActions } from "../components/ui/dialog";

const dirty = new Set<object>();
let replaying = false;
let opener: ((resume: () => void) => void) | null = null;

/** useDirty marks this part of the screen as having unsaved edits while on is true. */
export function useDirty(on: boolean) {
  useEffect(() => {
    if (!on) return;
    const token = {};
    dirty.add(token);
    return () => void dirty.delete(token);
  }, [on]);
}

/** hasEdits is whether any part of the screen has unsaved edits. */
export const hasEdits = () => dirty.size > 0;

/** clearDirty forgets every edit (after a save that moves on by itself). */
export function clearDirty() {
  dirty.clear();
}

function onClick(e: MouseEvent) {
  if (replaying || !dirty.size || !opener) return;
  const a = (e.target as Element | null)?.closest?.("a[href]") as HTMLAnchorElement | null;
  if (!a || e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey || a.target || a.hasAttribute("download")) return;
  if (a.closest("[aria-disabled='true']")) return;
  const url = new URL(a.href, location.href);
  if (!/^https?:$/.test(url.protocol) || url.origin !== location.origin) return;
  e.preventDefault();
  e.stopPropagation();
  opener(() => {
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

/** LeaveGuard is the question itself; render it once on a screen that has forms. */
export function LeaveGuard() {
  const [resume, setResume] = useState<(() => void) | null>(null);
  useEffect(() => {
    opener = (r) => setResume(() => r);
    document.addEventListener("click", onClick, true);
    window.addEventListener("beforeunload", onUnload);
    return () => {
      opener = null;
      document.removeEventListener("click", onClick, true);
      window.removeEventListener("beforeunload", onUnload);
    };
  }, []);
  return (
    <Dialog open={!!resume} onOpenChange={(o) => !o && setResume(null)} title="Leave without saving?" description="Your changes on this page are not saved yet.">
      <DialogActions>
        <Button onClick={() => setResume(null)}>Stay</Button>
        <Button
          variant="danger"
          onClick={() => {
            const r = resume;
            setResume(null);
            r?.();
          }}
        >
          Leave
        </Button>
      </DialogActions>
    </Dialog>
  );
}
