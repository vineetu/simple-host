// The judge's two walkthroughs (the old tours.js judge and judgeScoring, same
// words): Overview's, seen-state on the server (judge_walkthrough_done), and
// the scoring page's, seen-state in this browser per person (the server has
// no field for it; same key as before, so nobody sees it twice).
// Each opens once by itself, and again from Help's ?walkthrough=1.
import { useCallback, useEffect, useState } from "react";
import { useLocation, useNavigate } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Walkthrough, markSeenHere, seenHere, type WalkStep as TourStep } from "@kit/walkthrough/walkthrough";
import { useHack, type EventDetail } from "@/lib/client";
import { useMe } from "@/app/data";

const navLink = (s: string) => `aside nav a[href*="s=${s}"]`;
const barLink = (s: string) => `nav a[href*="s=${s}"]`;
const help = 'aside a[href*="walkthrough=1"]';

export function homeSteps(ev: EventDetail["event"], hero: "next" | "done" | "other"): TourStep[] {
  const open = !!ev.scoring_open && !ev.judging_locked_at;
  const notYet = !ev.scoring_open && !ev.judging_locked_at && !ev.results_published;
  return [
    { title: "Thank you for judging", body: "Here is how it works. It takes about 30 seconds." },
    { target: '[aria-label="Event status"]', when: () => open, title: "Is scoring open?", body: "This tells you whether you can score. It stays open until the organiser finishes judging." },
    { target: '[aria-label="Event status"]', when: () => notYet, title: "Is scoring open?", body: "It opens when the organiser starts judging. This bar shows it." },
    { target: '[data-tour="next"]', when: () => hero === "next", title: "Start here", body: "Score next opens the next team you haven’t scored." },
    { target: '[data-tour="next"]', when: () => hero === "done", title: "Start here", body: "Review my scores opens My teams, where you can change any score." },
    { target: '[data-tour="progress"]', title: "Your progress", body: "How many of your teams you have scored so far." },
    { target: navLink("queue"), phone: barLink("queue"), title: "My teams", body: "Every team you are scoring. Scored teams move to Scored. Teams where you know someone go under Teams I know." },
    { target: navLink("rubric"), phone: barLink("rubric"), title: "Rubric & brief", body: "What each criterion means and what the points are for. Read it once before you start." },
    { target: help, title: "Any time", body: "Open this walkthrough again from Help." },
  ];
}

export const scoringSteps: TourStep[] = [
  { target: '[data-tour="card"]', title: "Start with the card", body: "The team’s problem, solution, who it is for and what works today. A 20-second read." },
  { target: '[data-tour="video"]', title: "Watch the demo", body: "Press play to watch it here. Nothing loads until you do." },
  { target: '[data-tour="site"]', title: "See what they built", body: "The team’s website as they submitted it. Use Open for a new tab." },
  { target: '[data-tour="score"]', title: "Score each criterion", body: "Tap a score. It saves straight away." },
  { target: '[data-tour="comment"]', title: "Add a comment", body: "Write feedback any time. It saves once you tap a score, and the team sees it without your name." },
  {
    target: '[data-tour="save-next"]',
    title: "Save & next",
    body: "Your scores already saved themselves. This just moves you to the next team. On your last team it reads Save & finish. On a keyboard, N does the same and P goes back.",
  },
  {
    target: '[data-tour="conflict"]',
    title: "Know someone on this team?",
    body: "If you know someone on this team, press this. The team leaves your list and your score for it stops counting. You can undo it from My teams.",
  },
  { target: '[data-tour="prev"]', title: "Previous", body: "Go back to the team before this one. Disabled on your first team." },
];

// Once per page load per tour, so a refresh of the same screen (or the next team) does not pop it open again.
const tried: Record<string, boolean> = {};

const LOCAL = "sh-judge-scoring-tour-done";

/**
 * useTour returns the walkthrough for this screen, open once by itself when
 * `auto` (and not seen before), or when the address carries walkthrough=1.
 * `ready` is when the screen's targets are drawn.
 */
export function useTour(kind: "judge" | "judgeScoring", steps: TourStep[], ready: boolean, auto: boolean) {
  const api = useHack();
  const qc = useQueryClient();
  const uid = useMe().data;
  const { search, pathname } = useLocation();
  const navigate = useNavigate();
  const forced = new URLSearchParams(search).get("walkthrough") === "1";
  const prefs = useQuery({ queryKey: ["preferences"], queryFn: () => api.hack.preferences(), enabled: kind === "judge" && auto && !forced });
  const [open, setOpen] = useState(false);
  const localKey = LOCAL + "-" + (uid?.id || uid?.username || "anon");

  useEffect(() => {
    if (!ready || open) return;
    if (forced) {
      // The link's walkthrough=1 would reopen it on a reload.
      const p = new URLSearchParams(search);
      p.delete("walkthrough");
      navigate({ pathname, search: p.toString() ? "?" + p : "" }, { replace: true });
      setOpen(true);
      return;
    }
    if (!auto || tried[kind]) return;
    if (kind === "judge") {
      if (!prefs.isSuccess) return;
      tried[kind] = true;
      if (!prefs.data?.judge_walkthrough_done) setOpen(true);
    } else {
      if (!uid) return;
      tried[kind] = true;
      if (!seenHere(localKey)) setOpen(true);
    }
  }, [ready, open, forced, auto, kind, prefs.isSuccess, prefs.data, uid, localKey, search, pathname, navigate]);

  const done = useCallback(() => {
    setOpen(false);
    if (kind === "judge") {
      api.hack.setPreferences({ judge_walkthrough_done: true }).then(
        () => qc.setQueryData(["preferences"], (p: Record<string, unknown> | undefined) => ({ ...p, judge_walkthrough_done: true })),
        () => {},
      );
    } else markSeenHere(localKey);
  }, [api, qc, kind, localKey]);
  const close = useCallback(() => setOpen(false), []);

  return <Walkthrough steps={steps} open={open} onFinish={done} onClose={close} />;
}
