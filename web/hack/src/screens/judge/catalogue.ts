// The judge's four screens in every state, and their two walkthroughs. Ids
// are the UX atlas's screen.state ids. The judge is Siti on Fintech Jam.
import { refuse, type Answer, type Entry } from "@kit/catalogue/harness";
import { api, detail } from "@/catalogue/fixtures";
import type { EventDetail } from "@/lib/client";
import type { Checklist, Criterion, QueueRow } from "./data";

const SLUG = "fintech-jam";
const E = "/v1/hack/events/" + SLUG;
const home = `/e/${SLUG}/judge?s=home`;
const queueURL = `/e/${SLUG}/judge?s=queue`;
const rubricURL = `/e/${SLUG}/judge?s=rubric`;
const scoring = (team = "") => `/e/${SLUG}/judge?s=scoring` + (team ? "&team=" + team : "");

const criteria: Criterion[] = [
  { id: "c1", name: "Idea & impact", description: "Does it solve a real problem for real people?", max_points: 10, weight: 40 },
  { id: "c2", name: "Does it work", description: "Can you use it end to end today?", max_points: 10, weight: 35 },
  { id: "c3", name: "Design & clarity", max_points: 10, weight: 25 },
];

const rows: QueueRow[] = [
  {
    team_id: "t-pasar",
    team: { slug: "pasar", name: "Pasar" },
    title: "PasarPay",
    tagline: "Market stalls take QR payments without a bank account.",
    problem: "Stall owners lose sales when buyers carry no cash.",
    audience: "Traditional market sellers in Jakarta",
    tools: ["Claude", "Supabase"],
    maturity: "working",
    track: { name: "Payments" },
    description: "We started from interviews with twelve sellers at Pasar Santa.\n\nThe ledger runs on a shared sheet.",
    submitted_at: "2026-11-19T09:12:00Z",
    done: false,
    has_screenshot: true,
    video: { url: "https://youtu.be/abc123", host: "youtu.be", embed_url: "https://www.youtube-nocookie.com/embed/abc123" },
    site: { url: "https://pasar.fintech-jam.simple-hack.app/", preview_url: "https://pasar.fintech-jam.simple-hack.app/_preview/v2", host: "pasar.fintech-jam.simple-hack.app", frameable: true },
    site_url: "https://pasar.fintech-jam.simple-hack.app/",
    site_exists: true,
    pinned_url: "https://pasar.fintech-jam.simple-hack.app/_preview/v2",
    pinned_version: 2,
    code_url: "https://github.com/example/pasarpay",
  },
  {
    team_id: "t-arisan",
    team: { slug: "arisan", name: "Arisan Club" },
    title: "Arisan Club",
    tagline: "Rotating savings groups, kept honest by a shared log.",
    problem: "Groups argue over who paid in.",
    audience: "",
    tools: ["ChatGPT"],
    maturity: "partial",
    submitted_at: "2026-11-19T10:40:00Z",
    done: true,
    has_screenshot: true,
    video: { url: "https://www.dropbox.com/s/demo.mp4", host: "www.dropbox.com", embed_url: null },
    site: { url: "https://arisan.fintech-jam.simple-hack.app/", preview_url: null, host: "arisan.fintech-jam.simple-hack.app", frameable: false, not_frameable_why: "passcode" },
    site_url: "https://arisan.fintech-jam.simple-hack.app/",
    site_exists: true,
  },
  {
    team_id: "t-kopi",
    team: { slug: "kopi", name: "Kopi Ledger" },
    title: "",
    tagline: "",
    problem: "",
    tools: [],
    maturity: "concept",
    incomplete: true,
    done: false,
    has_screenshot: false,
    video: null,
    site: null,
    site_exists: false,
  },
];

const checklist = (o: Partial<Checklist> = {}): Checklist => ({
  items: [
    { id: "j.invite", label: "Accept the judge invite", state: "done" },
    { id: "j.rubric", label: "Read the rubric", state: "done", version: "v1" },
    { id: "j.conflicts", label: "Check who you know", state: "done" },
    { id: "j.scores", label: "Score each team", detail: "1 of 3 scored", actionable: true, link: { href: scoring() } },
    { id: "j.closes", label: "Judging closes", detail: "Sun 22 Nov, 7:00 pm", optional: true },
  ],
  next: { kind: "link", item_id: "j.scores", label: "Score each team", link: { href: scoring() } },
  ...o,
});
const setupChecklist = checklist({
  items: [
    { id: "j.invite", label: "Accept the judge invite", state: "done" },
    { id: "j.rubric", label: "Read the rubric", detail: "3 criteria, 30 points", actionable: true, link: { href: rubricURL }, version: "v1" },
    { id: "j.conflicts", label: "Check who you know", actionable: true, link: { href: queueURL + "&tab=conflicts" } },
    { id: "j.scores", label: "Score each team", detail: "1 of 3 scored", actionable: true, link: { href: scoring() } },
  ],
  next: { kind: "link", item_id: "j.rubric", label: "Read the rubric", link: { href: rubricURL } },
});

type Ev = Partial<EventDetail["event"]>;
const judge = (ev: Ev = {}, o: Partial<EventDetail> = {}) => {
  const d = detail(SLUG, { me: {}, needs_you: [], ...o });
  return { ...d, event: { ...d.event, about: "Build money tools for people banks leave out.\n\nOne weekend, in person in Jakarta.", rules: "Teams of up to four. Build during the event.", prizes: "Rp 20 juta for the winner.", ...ev } };
};

/** j is the judge's world; over wins for any path it names. Walkthroughs are seen unless an entry says otherwise. */
function j(over: Record<string, Answer> = {}, ev: Ev = {}, o: Partial<EventDetail> = {}) {
  return api({
    [E]: judge(ev, o),
    [E + "/judge/queue"]: { queue: rows, candidates: rows.map((r) => ({ team_id: r.team_id, team_name: r.team.name })), judge_first: "video", assignment_mode: "open" },
    [E + "/conflicts?mine=1"]: { conflicts: [] },
    [E + "/checklist?role=judge"]: { checklist: checklist() },
    [E + "/announcements"]: { announcements: [{ id: "a1", title: "Judges: start with the Payments track", body_html: "<p>Coffee is in room 2. Ask Dewi if a site will not load.</p>", pinned: true }] },
    [E + "/leave"]: { case: "judge", slug: SLUG, scored_teams: 1 },
    [E + "/rubric"]: { criteria, version: "v1" },
    [E + "/judge/scores/t-arisan"]: { scores: criteria.map((c, i) => ({ criterion_id: c.id, points: 6 + i })), comment: "Clear pitch. Show the log on a phone next time." },
    [E + "/judge/scores/t-pasar"]: { scores: [], comment: "" },
    [E + "/judge/scores/t-kopi"]: { scores: [], comment: "" },
    "/v1/hack/preferences": { judge_walkthrough_done: true },
    ...over,
  });
}
const queueOf = (list: QueueRow[], o: Record<string, unknown> = {}) => ({ [E + "/judge/queue"]: { queue: list, candidates: list.map((r) => ({ team_id: r.team_id, team_name: r.team.name })), judge_first: "video", assignment_mode: "open", ...o } });
const allDone = rows.map((r) => ({ ...r, done: true }));
const LOCKED: Ev = { judging_locked_at: "2026-11-21T12:00:00Z", scoring_open: false };
const fail = refuse(500, "error", "Something went wrong. Try again.");

// The scoring walkthrough's seen-state lives in this browser, per person.
const scoringSeen = (seen: boolean) => () => {
  try {
    if (seen) localStorage.setItem("sh-judge-scoring-tour-done-u-siti", "1");
    else localStorage.removeItem("sh-judge-scoring-tour-done-u-siti");
    sessionStorage.removeItem("sh-judge-order-" + SLUG);
  } catch {
    /* storage blocked */
  }
};
const s = (e: Omit<Entry, "before"> & { tour?: boolean }): Entry => ({ ...e, before: scoringSeen(!e.tour) });

export const entries: Entry[] = [
  // ---- Overview ----
  { id: "judge-home.loading", title: "Judge Overview: loading", url: home, api: j({ [E + "/judge/queue"]: "pending" }) },
  { id: "judge-home.error", title: "Judge Overview: the queue did not load", url: home, api: j({ [E + "/judge/queue"]: fail }) },
  {
    id: "judge-home.scoring-open",
    title: "Judge Overview: scoring open, 1 of 3 scored, with Needs you and a pinned announcement",
    url: home,
    api: j({}, {}, { needs_you: [{ id: "n1", kind: "judging_open", text: "Judging is open. Score your teams by Sunday.", section: "queue", dismissable: true }] }),
  },
  { id: "judge-home.setup-step", title: "Judge Overview: read the rubric first", url: home, api: j({ [E + "/checklist?role=judge"]: { checklist: setupChecklist } }) },
  { id: "judge-home.all-scored", title: "Judge Overview: all scored, still open", url: home, api: j(queueOf(allDone)) },
  { id: "judge-home.before-judging", title: "Judge Overview: scoring not open yet", url: home, api: j({}, { stage: "closed", scoring_open: false }) },
  { id: "judge-home.locked-done", title: "Judge Overview: locked, fully done", url: home, api: j(queueOf(allDone), LOCKED) },
  { id: "judge-home.locked-partial", title: "Judge Overview: locked before finishing", url: home, api: j({}, LOCKED) },
  { id: "judge-home.results", title: "Judge Overview: results are out", url: home, api: j({}, { stage: "results", scoring_open: false, results_published: true, judging_locked_at: "2026-11-21T12:00:00Z" }) },
  { id: "judge-home.nothing-assigned", title: "Judge Overview: nothing assigned", url: home, api: j(queueOf([])) },
  {
    id: "judge-home.everything-conflicted",
    title: "Judge Overview: a conflict with every team",
    url: home,
    api: j({ [E + "/judge/queue"]: { queue: [], candidates: rows.map((r) => ({ team_id: r.team_id, team_name: r.team.name, conflicted: true })) } }),
  },
  {
    id: "judge-home.event-ended",
    title: "Judge Overview: event ended, leaving is off",
    url: home,
    api: j({ [E + "/leave"]: { case: "judge", blocked: { code: "event_closed", message: "This event has ended." } } }, { stage: "archived", scoring_open: false, results_published: true }),
  },
  { id: "judge-home.leave", title: "Judge Overview: leave this event", url: home, api: j(), steps: [{ click: "text=Leave event" }] },
  { id: "judge-home.walkthrough", title: "Judge Overview: the walkthrough, first step", url: home, api: j({ "/v1/hack/preferences": { judge_walkthrough_done: false } }) },
  {
    id: "judge-home.walkthrough-next",
    title: "Judge Overview: the walkthrough, on Start here",
    url: home,
    api: j({ "/v1/hack/preferences": { judge_walkthrough_done: false } }),
    steps: [{ click: "text=Next" }, { click: "text=Next" }],
  },

  // ---- My teams ----
  { id: "judge-queue.loading", title: "My teams: loading", url: queueURL, api: j({ [E + "/judge/queue"]: "pending" }) },
  { id: "judge-queue.error", title: "My teams: did not load", url: queueURL, api: j({ [E + "/conflicts?mine=1"]: fail }) },
  { id: "judge-queue.before-judging", title: "My teams: scoring not open", url: queueURL, api: j({}, { stage: "closed", scoring_open: false }) },
  { id: "judge-queue.mixed", title: "My teams: some scored, some left", url: queueURL, api: j() },
  { id: "judge-queue.scored", title: "My teams: the Scored tab", url: queueURL + "&tab=scored", api: j() },
  { id: "judge-queue.screenshot", title: "My teams: a screenshot open", url: queueURL, api: j(), steps: [{ click: "text=View screenshot" }] },
  {
    id: "judge-queue.conflicts",
    title: "My teams: Teams I know, one declared, not yet said none",
    url: queueURL + "&tab=conflicts",
    api: j({
      [E + "/conflicts?mine=1"]: { conflicts: [{ team_id: "t-kopi", team_name: "Kopi Ledger", had_score: false }] },
      [E + "/checklist?role=judge"]: { checklist: setupChecklist },
    }),
  },

  { id: "judge-queue.locked", title: "My teams: locked, read-only", url: queueURL, api: j({}, LOCKED) },
  { id: "judge-queue.results", title: "My teams: results are out", url: queueURL + "&tab=scored", api: j({}, { stage: "results", scoring_open: false, results_published: true }) },
  { id: "judge-queue.all-done", title: "My teams: everyone scored", url: queueURL + "&tab=to-score", api: j(queueOf(allDone)) },
  { id: "judge-queue.nothing-yet", title: "My teams: nothing to judge yet (assigned mode)", url: queueURL, api: j({ [E + "/judge/queue"]: { queue: [], candidates: [], assignment_mode: "assigned" } }) },
  {
    id: "judge-queue.all-conflicted",
    title: "My teams: a conflict with everyone",
    url: queueURL + "&tab=to-score",
    api: j({
      [E + "/judge/queue"]: { queue: [], candidates: rows.map((r) => ({ team_id: r.team_id, team_name: r.team.name, conflicted: true })) },
      [E + "/conflicts?mine=1"]: { conflicts: rows.map((r) => ({ team_id: r.team_id, team_name: r.team.name })) },
    }),
  },

  // ---- Scoring ----
  s({ id: "judge-scoring.loading", title: "Scoring: loading", url: scoring("t-pasar"), api: j({ [E + "/judge/queue"]: "pending" }) }),
  s({ id: "judge-scoring.error", title: "Scoring: the queue did not load", url: scoring("t-pasar"), api: j({ [E + "/judge/queue"]: fail }) }),
  s({ id: "judge-scoring.nothing", title: "Scoring: nothing assigned", url: scoring(), api: j(queueOf([])) }),
  s({ id: "judge-scoring.no-rubric", title: "Scoring: no rubric yet", url: scoring("t-pasar"), api: j({ [E + "/rubric"]: { criteria: [] } }) }),
  s({ id: "judge-scoring.before-judging", title: "Scoring: not open yet", url: scoring("t-pasar"), api: j({}, { stage: "closed", scoring_open: false }) }),
  s({ id: "judge-scoring.default", title: "Scoring: video plays here, site framed", url: scoring("t-pasar"), api: j() }),
  s({ id: "judge-scoring.playing", title: "Scoring: the video, after Play", url: scoring("t-pasar"), api: j(), steps: [{ click: "[data-play]" }] }),
  s({ id: "judge-scoring.elsewhere", title: "Scoring: video elsewhere, site behind a passcode; already scored", url: scoring("t-arisan"), api: j() }),
  s({ id: "judge-scoring.website-first", title: "Scoring: website first, then code, then video", url: scoring("t-pasar"), api: j(queueOf(rows, { judge_first: "site" })) }),
  s({ id: "judge-scoring.no-media", title: "Scoring: no video, no site, incomplete; the last team", url: scoring("t-kopi"), api: j() }),
  s({ id: "judge-scoring.unscored-guard", title: "Scoring: Save & next with criteria left", url: scoring("t-pasar"), api: j(), steps: [{ click: "text=Save & next" }] }),
  s({ id: "judge-scoring.score-saved", title: "Scoring: a score tapped and saved", url: scoring("t-pasar"), api: j({ ["PUT " + E + "/judge/scores/t-pasar"]: { scores: [{ criterion_id: "c1", points: 7 }], comment: "", complete: false } }), steps: [{ click: "[aria-label='Idea & impact'] [role=radio]@7" }] }),
  s({ id: "judge-scoring.locked", title: "Scoring: locked, read-only", url: scoring("t-arisan"), api: j({}, LOCKED) }),
  s({ id: "judge-scoring.walkthrough", title: "Scoring: the walkthrough, first step", url: scoring("t-pasar"), api: j(), tour: true }),

  // ---- Rubric ----
  { id: "judge-rubric.loading", title: "Rubric: loading", url: rubricURL, api: j({ [E + "/rubric"]: "pending" }) },
  { id: "judge-rubric.error", title: "Rubric: did not load", url: rubricURL, api: j({ [E + "/rubric"]: fail }) },
  { id: "judge-rubric.no-rubric", title: "Rubric: none yet", url: rubricURL, api: j({ [E + "/rubric"]: { criteria: [] } }, { about: "", rules: "", prizes: "" }) },
  { id: "judge-rubric.default", title: "Rubric: saved, not read yet", url: rubricURL, api: j({ [E + "/checklist?role=judge"]: { checklist: setupChecklist } }) },
  { id: "judge-rubric.read", title: "Rubric: already read", url: rubricURL, api: j() },
  {
    id: "judge-rubric.acknowledged",
    title: "Rubric: I've read it, pressed",
    url: rubricURL,
    api: j({ [E + "/checklist?role=judge"]: { checklist: setupChecklist }, ["POST " + E + "/checklist/acknowledgements/j.rubric"]: { checklist: checklist() } }),
    steps: [{ click: "text=I've read it" }],
  },
];
