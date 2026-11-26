// The organiser walkthrough on Home (?walkthrough=1 opens it): its first
// step (no target, in the middle), one on the page, and one on the rail.
import type { Entry } from "@kit/catalogue/harness";
import { E, orgApi, url } from "./fx";

const u = url("overview", "&walkthrough=1");
const a = orgApi({ ["GET " + E("assistant")]: { enabled: false }, "/v1/hack/preferences": { organiser_walkthrough_done: true } });
const next = { click: "text=Next" };

export const entries: Entry[] = [
  { id: "organiser-overview.walkthrough-1", title: "Walkthrough: welcome", url: u, api: a },
  { id: "organiser-overview.walkthrough-2", title: "Walkthrough: stage and next deadline", url: u, api: a, steps: [next] },
  { id: "organiser-overview.walkthrough-3", title: "Walkthrough: the next step", url: u, api: a, steps: [next, next] },
  { id: "organiser-overview.walkthrough-rail", title: "Walkthrough: a rail link (More on a phone)", url: u, api: a, steps: [next, next, next, next, next] },
];
