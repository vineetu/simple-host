// Results in every state (atlas organiser-results).
import { refuse, type Entry } from "@kit/catalogue/harness";
import { E, org, orgApi, url } from "./fx";

const u = url("results");
const off = { ["GET " + E("assistant")]: { enabled: false } };
const rows = [
  { team_id: "t1", team_name: "Lumbung", rank: 1, total: 8.6 },
  { team_id: "t2", team_name: "Kopi Club", rank: 2, total: 7.9 },
  { team_id: "t3", team_name: "Ojek AI", rank: null, total: null },
];
const ev = org({}, { stage: "results", results_published: true, results_published_at: "2026-11-19T11:00:00Z" });

export const entries: Entry[] = [
  { id: "organiser-results.loading", title: "Results: loading", url: u, api: orgApi({ ...off, [E("results")]: "pending" }) },
  { id: "organiser-results.not-published", title: "Results: not published", url: u, api: orgApi({ ...off, [E("results")]: {}, [E("judging/preview")]: { results: rows } }, org({}, { stage: "results" })) },
  { id: "organiser-results.published", title: "Results: published", url: u, api: orgApi({ ...off, [E("results")]: { results: rows }, [E("judging/preview")]: { results: rows } }, ev) },
  {
    id: "organiser-results.scores-changed",
    title: "Results: published scores changed",
    url: u,
    api: orgApi({ ...off, [E("results")]: { results: rows }, [E("judging/preview")]: { results: rows.map((r) => (r.team_id === "t3" ? { ...r, rank: 3, total: 6.2 } : r)) } }, ev),
  },
  {
    id: "organiser-results.preview",
    title: "Results: the current standings opened",
    url: u,
    api: orgApi({ ...off, [E("results")]: { results: rows }, [E("judging/preview")]: { results: rows } }, ev),
    steps: [{ click: "summary" }],
  },
  {
    id: "organiser-results.preview-failed",
    title: "Results: the standings did not load",
    url: u,
    api: orgApi({ ...off, [E("results")]: {}, [E("judging/preview")]: refuse(500, "error", "Something went wrong. Try again.") }, org({}, { stage: "judging" })),
    steps: [{ click: "summary" }],
  },
  {
    id: "organiser-results.downloaded",
    title: "Results: a download saved (the toast)",
    url: u,
    api: orgApi({ ...off, [E("results")]: { results: rows }, [E("judging/preview")]: { results: rows } }, ev),
    steps: [{ click: "text=Results (CSV)" }],
  },
];
