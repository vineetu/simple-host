// The participant walkthrough on Home (atlas walkthroughs.participant): opens
// itself once, the first time an approved participant reaches Home, and again
// from Help's "Replay walkthrough" (?walkthrough=1). Seen is kept in this
// browser per person, as before: the preferences API has no participant field.
import { useEffect, useRef, useState } from "react";
import { useLocation, useNavigate } from "react-router";
import { Tour, firstShowable, type TourStep } from "./tour";

// The shell's parts (app/HackShell.tsx): the rail's links on a wide screen,
// the bottom bar (or its More button) on a phone.
const rail = (s: string) => `aside a[href*="s=${s}"]`;
const bar = (s: string) => `nav.fixed a[href*="s=${s}"], nav.fixed button`;

export const steps: TourStep[] = [
  { title: "Welcome", body: "This is Overview. It always says the next thing to do. Here is a one-minute look around." },
  { target: '[aria-label="Event status"]', title: "Stage and deadline", body: "The event stage and the next deadline are always shown here." },
  { target: '[data-tour="next"]', title: "Your next step", body: "This always shows what to do next for where the event is now." },
  { target: '[data-tour="entry-checklist"]', title: "Your entry", body: "The four steps to a submitted entry, with a live countdown to your deadline." },
  { target: rail("schedule"), phone: bar("schedule"), title: "Schedule", body: "What is happening and when." },
  { target: rail("announcements"), phone: bar("announcements"), title: "Announcements", body: "Updates from the organiser." },
  { target: rail("team"), phone: bar("team"), title: "Team", body: "Find or start your team here. If team finding is on, you can see teams looking for people." },
  { target: rail("entry"), phone: bar("entry"), title: "Entry", body: "Fill in what you built. Teammates can edit it too. Judges see this." },
  { target: rail("website"), phone: bar("website"), title: "Website", body: "Publish your team’s site from an AI app or upload a zip." },
  { target: rail("results"), phone: bar("results"), title: "Results", body: "See the winners and how your team placed." },
  { target: 'aside a[href*="walkthrough=1"]', phone: "nav.fixed button", title: "Any time", body: "Open this walkthrough again from Help." },
];

const seenKey = (uid: string) => "sh-participant-tour-done-" + uid;
function seen(uid: string): boolean {
  try {
    return localStorage.getItem(seenKey(uid)) === "1";
  } catch {
    return false;
  }
}
function markSeen(uid: string) {
  try {
    localStorage.setItem(seenKey(uid), "1");
  } catch {
    /* storage blocked */
  }
}

// Once per page load: coming back to Home in the same visit does not reopen it.
let tried = false;

/** useWalkthrough returns the tour to render once Home's content is on screen (ready). */
export function useWalkthrough(uid: string | undefined, ready: boolean) {
  const { pathname, search } = useLocation();
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);
  const alive = useRef(true);
  const forced = new URLSearchParams(search).get("walkthrough") === "1";
  useEffect(() => {
    if (!ready || !uid) return;
    if (forced) {
      // The replay link's ?walkthrough=1 comes off, so a reload does not reopen it.
      const q = new URLSearchParams(search);
      q.delete("walkthrough");
      navigate(pathname + (q.size ? "?" + q : ""), { replace: true });
    } else if (tried || seen(uid)) return;
    tried = true;
    // After the frame: the shell and the cards are laid out, so the targets can be found.
    requestAnimationFrame(() => alive.current && firstShowable(steps) !== -1 && setOpen(true));
  }, [ready, uid, forced, pathname, search, navigate]);
  useEffect(() => {
    alive.current = true;
    return () => void (alive.current = false);
  }, []);
  if (!open || !uid) return null;
  return (
    <Tour
      steps={steps}
      onDone={() => {
        markSeen(uid);
        setOpen(false);
      }}
      onGone={() => setOpen(false)}
    />
  );
}
