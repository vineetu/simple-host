// The Ask panel's states (atlas organiser-overview "Ask: …"), shown over People.
// The question goes out through transport.send, which these entries replace
// with a fake stream (before() runs ahead of the screen).
import type { Entry } from "@kit/catalogue/harness";
import { sse, transport } from "../data-assistant";
import { E, orgApi, url } from "./fx";

const u = url("people");
const quiet = { [E("applications")]: [], [E("people")]: [] };
const on = (status: Record<string, unknown>) => orgApi({ ...quiet, ["GET " + E("assistant")]: { enabled: true, remaining_today: 18, daily_limit: 30, ...status } });
const clear = () => {
  try {
    for (const k of Object.keys(localStorage)) if (k.startsWith("sh-assistant-")) localStorage.removeItem(k);
  } catch {
    /* storage blocked */
  }
};

export const entries: Entry[] = [
  {
    id: "organiser-overview.ask-not-set-up",
    title: "Ask: not set up (no Ask button)",
    url: u,
    api: orgApi({ ...quiet, ["GET " + E("assistant")]: { enabled: false } }),
  },
  { id: "organiser-overview.ask-button", title: "Ask: the floating button", url: u, api: on({}), before: clear },
  {
    id: "organiser-overview.ask-empty",
    title: "Ask: empty thread with starters",
    url: u,
    api: on({}),
    before: clear,
    steps: [{ click: "[data-tour='assistant']" }],
  },
  {
    id: "organiser-overview.ask-streaming",
    title: "Ask: streaming a reply",
    url: u,
    api: on({}),
    before: () => {
      clear();
      transport.send = async () => sse([{ delta: "Close entries in **Home** with the next-step button. " }, { delta: "Teams with their own extension keep it. See [Entries](/e/green-campus/manage?s=entries)." }], true);
    },
    steps: [{ click: "[data-tour='assistant']" }, { click: "text=What happens when I close entries?" }],
  },
  {
    id: "organiser-overview.ask-answered",
    title: "Ask: an answer, with Copy and Retry",
    url: u,
    api: on({ remaining_today: 4 }),
    before: () => {
      clear();
      transport.send = async () =>
        sse([{ delta: "Both links are on **Home**, under Share your event:\n\n- the join link for participants\n- the judge link for judges\n\nMake new ones in [Settings → Links](/e/green-campus/manage?s=settings&tab=links)." }, { done: true, remaining_today: 3 }]);
    },
    steps: [{ click: "[data-tour='assistant']" }, { click: "text=Where do I find the join and judge links?" }],
  },
  {
    id: "organiser-overview.ask-limit",
    title: "Ask: daily or monthly limit reached",
    url: u,
    api: on({ remaining_today: 0, daily_limit: 30 }),
    before: clear,
    steps: [{ click: "[data-tour='assistant']" }],
  },
  {
    id: "organiser-overview.ask-failed",
    title: "Ask: the answer failed, with Retry",
    url: u,
    api: on({}),
    before: () => {
      clear();
      transport.send = async () => sse([{ error: true, message: "The assistant couldn't answer. Try again." }]);
    },
    steps: [{ click: "[data-tour='assistant']" }, { click: "text=What happens when I close entries?" }],
  },
];
