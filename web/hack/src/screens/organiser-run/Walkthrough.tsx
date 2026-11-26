// The organiser's walkthrough (tours.js "organiser", the atlas's
// walkthroughs.organiser): opens by itself the first time an organiser is on
// Home (once per page load), and always with ?walkthrough=1 (Help → Replay
// walkthrough), which is then taken off the address. "Seen" is the server
// preference organiser_walkthrough_done, so it follows the person.
// It waits for the screen (ready) and the Ask button's status, so steps whose
// target is not there yet are not skipped by mistake.
import { useCallback, useEffect, useRef, useState } from "react";
import { useLocation, useNavigate } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { Walkthrough, type WalkStep } from "@kit/walkthrough/walkthrough";
import { useHack } from "@/lib/client";
import { useAssistantStatus } from "./Assistant";

const rail = (section: string) => `aside a[href*="s=${section}"]`;
/** The phone's More button (every rail link is in its sheet). */
const MORE = 'nav[aria-label="Main"] > button';

export const ORGANISER_STEPS: WalkStep[] = [
  { title: "Welcome to your event", body: "This is Home, your event’s home. It always says the next thing to do. Here is a one-minute look around." },
  { target: '[aria-label="Event status"]', title: "Stage and next deadline", body: "The stage and the next deadline are always shown here. The stage decides what participants and judges can do right now." },
  { target: '[data-tour="next-move"]', title: "Your next step", body: "This button opens the next checklist task or moves the event forward. Stage changes ask you to confirm." },
  { target: '[data-tour="assistant"]', title: "Ask about your event", body: "Want to know more about a setting? Ask here, from any screen. It answers in a line or two and links to the place to change it." },
  { target: '[data-tour="change-stage"]', title: "Move the event on", body: "To move to any other stage, or back, use Change stage. Each change says what will happen before you confirm." },
  { target: '[data-tour="share"]', title: "Invite people", body: "Send the join link to participants and the judge link to judges. Make a new link in Settings → Links if a link leaked." },
  { target: rail("event-page"), phone: MORE, title: "Event page", body: "What visitors see before they sign up, one piece at a time: basics, about, schedule, sponsors, FAQ, look, and your own website if you have one." },
  { target: rail("announcements"), phone: MORE, title: "Announcements", body: "Post updates for everyone in the event. Tick the box to email every participant too." },
  { target: rail("settings"), phone: MORE, title: "Settings", body: "Set sign-up, team and entry rules, judging, voting, result visibility and directory listing here. Organiser details and notifications live here too." },
  { target: rail("people"), phone: MORE, title: "People", body: "Review applications and manage the people who signed up." },
  { target: rail("teams"), phone: MORE, title: "Teams", body: "Watch teams form and place people who are not on a team yet." },
  { target: rail("entries"), phone: MORE, title: "Entries", body: "Review entries and give one team more time." },
  { target: rail("judging"), phone: MORE, title: "Judging", body: "Assign judges, watch progress, and finish judging from Home when every score is in." },
  { target: rail("results"), phone: MORE, title: "Results", body: "Preview the standings, choose what the public page shows and download exports. Publish results from Home." },
  { target: 'aside a[href*="walkthrough=1"]', phone: MORE, title: "Any time", body: "Open this walkthrough again from Help." },
];

let autoTried = false;

/** OrganiserWalkthrough: render it on Home once the screen has drawn (ready); force is ?walkthrough=1. */
export function OrganiserWalkthrough({ slug, force, ready = true }: { slug: string; force: boolean; ready?: boolean }) {
  const api = useHack();
  const navigate = useNavigate();
  const { pathname, search } = useLocation();
  const [open, setOpen] = useState(false);
  const [forced] = useState(force);
  const alive = useRef(true);
  useEffect(() => {
    alive.current = true;
    return () => void (alive.current = false);
  }, []);
  const prefs = useQuery({ queryKey: ["preferences"], queryFn: () => api.hack.preferences(), enabled: !forced && !autoTried });
  const ask = useAssistantStatus(slug);
  const settled = ready && !ask.isPending && (forced || !prefs.isPending);

  useEffect(() => {
    if (!settled || open) return;
    if (forced) {
      const q = new URLSearchParams(search);
      if (q.has("walkthrough")) {
        q.delete("walkthrough");
        const s = q.toString();
        navigate(pathname + (s ? "?" + s : ""), { replace: true });
      }
    } else {
      if (autoTried) return;
      autoTried = true;
      if (prefs.isError || prefs.data?.organiser_walkthrough_done) return;
    }
    // A frame for the screen's last pieces to draw before the first step looks for its target.
    requestAnimationFrame(() => alive.current && setOpen(true));
  }, [settled]); // eslint-disable-line react-hooks/exhaustive-deps

  const onFinish = useCallback(() => {
    api.hack.setPreferences({ organiser_walkthrough_done: true }).catch(() => {});
  }, [api]);
  const onClose = useCallback(() => setOpen(false), []);
  return <Walkthrough steps={ORGANISER_STEPS} open={open} onFinish={onFinish} onClose={onClose} />;
}
