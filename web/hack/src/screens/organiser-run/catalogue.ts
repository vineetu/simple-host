// Every state of the organiser's running-the-event screens, one file per
// screen under cat/. Ids are <screen-id>.<state> (the UX atlas's screens).
import type { Entry } from "@kit/catalogue/harness";
import { entries as overview } from "./cat/overview";
import { entries as people } from "./cat/people";
import { entries as teams } from "./cat/teams";
import { entries as teamDetail } from "./cat/team-detail";
import { entries as entriesList } from "./cat/entries";
import { entries as entryDetail } from "./cat/entry-detail";
import { entries as announcements } from "./cat/announcements";
import { entries as results } from "./cat/results";
import { entries as assistant } from "./cat/assistant";
import { entries as walkthrough } from "./cat/walkthrough";

export const entries: Entry[] = [...overview, ...people, ...teams, ...teamDetail, ...entriesList, ...entryDetail, ...announcements, ...results, ...assistant, ...walkthrough];
