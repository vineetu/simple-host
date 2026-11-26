// Entry in every state: loading, no team, did not load, the draft (missing
// lines, ready), a refused submit, a refused save, submitted (and the rules
// tightened since), a teammate's edit at the same time, each dialog, and the
// read-only states after the deadline.
import { refuse, type Entry } from "@kit/catalogue/harness";
import { api } from "@/catalogue/fixtures";
import { LUMBUNG, on } from "../team/catalogue";
import type { EntryData } from "./data";

const SLUG = "jakarta-ai-night";
const url = `/e/${SLUG}/team?s=entry`;
const E = "/v1/hack/events/" + SLUG;
const ENTRY = E + "/entry";
const RUBRIC = E + "/rubric";

const rubric = {
  criteria: [
    { name: "Problem", description: "Is it a real problem, for real people?", max_points: 10 },
    { name: "Execution", description: "Does it work today?", max_points: 10 },
    { name: "Design", max_points: 5 },
  ],
};
const rules = { fields: { problem: "required", tagline: "required", audience: "optional", track: "required", tools: "required", maturity: "required", video_url: "one_of", site: "one_of", code_url: "optional", screenshot: "optional", description: "optional" }, summary: "a video or a published website" } as const;

const draft: EntryData = {
  editable: true,
  status: "draft",
  complete: false,
  missing: ["tools", "one_of"],
  progress: { steps: [{ id: "summary", filled: 5, of: 6 }] },
  rules,
  site: { exists: false },
  entry: {
    title: "Rice Ledger",
    problem: "Village co-ops lose track of who lent what.",
    tagline: "A shared ledger farmers keep by voice message.",
    audience: "Rice co-ops in West Java",
    track: { slug: "food", name: "Food" },
    tools: [],
    maturity: "partial",
    video_url: "",
    code_url: "",
    description: "",
    has_screenshot: false,
    updated_at: "2026-11-20T09:00:00Z",
  },
};
const ready: EntryData = {
  ...draft,
  complete: true,
  missing: [],
  progress: { steps: [{ id: "summary", filled: 6, of: 6 }] },
  site: { exists: true, live_version: 3 },
  entry: { ...draft.entry, tools: ["Claude", "Google Sheets", "Whisper"], maturity: "working", video_url: "https://youtu.be/abc", has_screenshot: true },
};
const submitted: EntryData = { ...ready, status: "submitted", submitted_at: "2026-11-20T07:02:00Z", submitted_by: "Siti Rahma" };

const page = (entry: unknown, o: Record<string, unknown> = {}) => api({ ["GET " + E]: on(LUMBUNG), [ENTRY]: entry, [RUBRIC]: rubric, ...o });
const closed = (data: EntryData) => page({ ...data, editable: false }, { ["GET " + E]: on({ ...LUMBUNG, frozen: true }, "judging") });

export const entries: Entry[] = [
  { id: "participant-entry.loading", title: "Entry: loading", url, api: page("pending") },
  { id: "participant-entry.no-team", title: "Entry: no team yet", url, api: page(refuse(409, "no_team", "Join or start a team first."), { ["GET " + E]: on(null) }) },
  { id: "participant-entry.error", title: "Entry: did not load", url, api: page(refuse(500, "error", "Something went wrong. Try again.")) },
  { id: "participant-entry.draft-missing", title: "Entry: draft with lines missing", url, api: page(draft) },
  { id: "participant-entry.draft-ready", title: "Entry: draft, ready to submit (no rubric)", url, api: page(ready, { [RUBRIC]: { criteria: [] } }) },
  {
    id: "participant-entry.submit-refused",
    title: "Entry: submit refused, what's missing",
    url,
    api: page(draft, { [`POST ${ENTRY}/submit`]: refuse(422, "entry_incomplete", "Add these first: Tools and AI, a video or a published website.", { missing: ["tools", "one_of"] }) }),
    steps: [{ click: "text=Submit entry" }],
  },
  {
    id: "participant-entry.save-refused",
    title: "Entry: a save refused on one line",
    url,
    api: page(draft, { [`PUT ${ENTRY}`]: refuse(400, "invalid_url", "invalid url") }),
    steps: [{ fill: ["#en-video_url", "youtube dot com"] }, { click: "text=Save draft" }],
  },
  { id: "participant-entry.tools", title: "Entry: tools typed and suggested", url, api: page(draft), steps: [{ click: "text=+ Claude" }, { click: "text=+ Figma" }, { fill: ["#en-tools-input", "Whisp"] }] },
  { id: "participant-entry.submitted", title: "Entry: submitted, still editable", url, api: page(submitted) },
  { id: "participant-entry.tightened", title: "Entry: submitted, the rules ask for more now", url, api: page({ ...submitted, complete: false, missing: ["code_url"] }) },
  { id: "participant-entry.unsubmit", title: "Entry: move back to draft", url, api: page(submitted), steps: [{ click: "text=Move back to draft" }] },
  { id: "participant-entry.remove-screenshot", title: "Entry: remove the screenshot", url, api: page(ready), steps: [{ click: "text=Remove screenshot" }] },
  {
    id: "participant-entry.conflict",
    title: "Entry: a teammate saved at the same time",
    url,
    api: page(draft, {
      [`PUT ${ENTRY}`]: refuse(409, "entry_changed", "A teammate changed this entry.", { entry: { ...draft.entry, title: "Rice Ledger by voice", audience: "Farmers' co-ops", updated_at: "2026-11-20T09:30:00Z" } }),
    }),
    steps: [{ fill: ["#en-title", "Rice Ledger 2"] }, { click: "text=Save draft" }],
  },
  { id: "participant-entry.closed-submitted", title: "Entry: entries closed, submitted", url, api: closed(submitted) },
  { id: "participant-entry.closed-auto", title: "Entry: submitted automatically at the deadline", url, api: closed({ ...submitted, submit_auto: true }) },
  { id: "participant-entry.closed-incomplete", title: "Entry: not submitted at the deadline, incomplete", url, api: closed({ ...draft, status: "not_submitted" }) },
  { id: "participant-entry.closed-settling", title: "Entry: deadline passed, not settled yet", url, api: closed({ ...ready, refusal: "submissions_closed" }) },
  { id: "participant-entry.closed-nothing", title: "Entry: nothing entered by the deadline", url, api: closed({ status: "not_submitted", refusal: "submissions_closed", rules, entry: {} }) },
];
