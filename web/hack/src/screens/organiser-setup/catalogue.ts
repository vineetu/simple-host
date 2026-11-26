// Every state of the organiser's setup screens, one file per screen.
import type { Entry } from "@kit/catalogue/harness";
import { entries as eventPage } from "./catalogue-event-page";
import { entries as settings } from "./catalogue-settings";
import { entries as judging } from "./catalogue-judging";
import { entries as assignments } from "./catalogue-assignments";

export const entries: Entry[] = [...eventPage, ...settings, ...judging, ...assignments];
