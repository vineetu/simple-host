// People's choice in every state.
import { refuse, type Entry } from "@kit/catalogue/harness";
import { api } from "@/catalogue/fixtures";
import type { Ballot } from "@/lib/client/voting";

const out = () => localStorage.removeItem("apiKey");
const V = "/v1/hack/events/jakarta-ai-night/vote";
const MY = "/v1/hack/events/jakarta-ai-night/my-vote";
const options: Ballot["options"] = [
  { team: "lumbung", team_name: "Lumbung", title: "Rice Ledger", tagline: "A shared ledger farmers keep by voice message.", url: "https://lumbung.jakarta-ai-night.simple-hack.app/" },
  { team: "ojek-ai", team_name: "Ojek AI", title: "Ride Buddy", url: "https://ojek-ai.jakarta-ai-night.simple-hack.app/" },
  { team: "kopi", team_name: "Kopi Club", title: "Bean Map", tagline: "Where the good coffee is.", url: "https://kopi.jakarta-ai-night.simple-hack.app/" },
];
const ballot = (o: Partial<Ballot>): Ballot => ({
  title: "Jakarta AI Night",
  status: "open",
  time_zone: "Asia/Jakarta",
  opens_at: "2026-11-20T09:00:00Z",
  closes_at: "2026-11-21T09:00:00Z",
  eligibility: "participants",
  options,
  ...o,
});
const url = "/e/jakarta-ai-night/vote";

export const entries: Entry[] = [
  { id: "public-vote.loading", title: "People's choice: loading", url, api: api({ [V]: "pending" }) },
  { id: "public-vote.signed-out", title: "People's choice: open, signed out", url, api: api({ [V]: ballot({}) }), before: out },
  { id: "public-vote.open", title: "People's choice: open, one vote cast, own team marked", url, api: api({ [V]: ballot({}), [MY]: { team: "ojek-ai", eligible: true, own_team: "lumbung" } }) },
  { id: "public-vote.ineligible", title: "People's choice: open, this person may not vote", url, api: api({ [V]: ballot({}), [MY]: { eligible: false } }) },
  { id: "public-vote.check-failed", title: "People's choice: could not check the vote", url, api: api({ [V]: ballot({}), [MY]: refuse(500, "error", "Something went wrong. Try again.") }) },
  { id: "public-vote.no-teams", title: "People's choice: open, no team sites yet", url, api: api({ [V]: ballot({ options: [] }), [MY]: { eligible: true } }) },
  { id: "public-vote.upcoming", title: "People's choice: opens soon", url, api: api({ [V]: ballot({ status: "upcoming" }) }) },
  {
    id: "public-vote.closed",
    title: "People's choice: closed, the final votes",
    url,
    api: api({
      [V]: ballot({ status: "closed", ranking: [{ team: "lumbung", team_name: "Lumbung", votes: 12 }, { team: "kopi", team_name: "Kopi Club", votes: 1 }, { team: "ojek-ai", team_name: "Ojek AI", votes: 0 }] }),
      [MY]: { team: "kopi", eligible: true },
    }),
  },
  { id: "public-vote.off", title: "People's choice: not on for this event", url, api: api({ [V]: ballot({ status: "off", options: [], opens_at: null, closes_at: null }) }) },
  { id: "public-vote.error", title: "People's choice: unavailable", url, api: api({ [V]: refuse(404, "not_found", "Voting isn't set up for this event.") }), before: out },
  {
    id: "public-vote.voted",
    title: "People's choice: after voting, the toast",
    url,
    api: (m, p, b) => (m === "PUT" && p === V ? { changed: true } : api({ [V]: ballot({}), [MY]: { team: "ojek-ai", eligible: true, own_team: "lumbung" } })(m, p, b)),
    steps: [{ click: "text=Vote for this team: Bean Map" }],
  },
];
