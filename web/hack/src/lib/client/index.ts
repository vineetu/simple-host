// The one typed client for /v1/hack and the platform routes the Hack app uses,
// grouped by area (one file each, see SCREENS.md section 3). Screens call it as
//   const api = useHack();  api.event.get(slug)
// Builders type a payload in its area file when the screen that reads it is rebuilt.
// Shared file: only ever add an area here (one import line and one key).
import { useMemo } from "react";
import { bind, useApi, type Call } from "@kit/api/call";
import * as auth from "./auth";
import * as me from "./me";
import * as hack from "./hack";
import * as invites from "./invites";
import * as event from "./event";
import * as people from "./people";
import * as content from "./content";
import * as messages from "./messages";
import * as teams from "./teams";
import * as team from "./team";
import * as entries from "./entries";
import * as judging from "./judging";
import * as voting from "./voting";
import * as exports from "./exports";
import * as webhooks from "./webhooks";
import * as sites from "./sites";
import * as assistant from "./assistant";
import * as operator from "./operator";

export function client(call: Call) {
  return {
    auth: bind(auth, call),
    me: bind(me, call),
    hack: bind(hack, call),
    invites: bind(invites, call),
    event: bind(event, call),
    people: bind(people, call),
    content: bind(content, call),
    messages: bind(messages, call),
    teams: bind(teams, call),
    team: bind(team, call),
    entries: bind(entries, call),
    judging: bind(judging, call),
    voting: bind(voting, call),
    exports: bind(exports, call),
    webhooks: bind(webhooks, call),
    sites: bind(sites, call),
    assistant: bind(assistant, call),
    operator: bind(operator, call),
  };
}
export type HackClient = ReturnType<typeof client>;

/** useHack is the client on the nearest ApiProvider's Call. */
export function useHack(): HackClient {
  const call = useApi();
  return useMemo(() => client(call), [call]);
}

/** Paths for bytes (screenshots, downloads, uploads), which are not JSON calls. */
export const paths = {
  submissionScreenshot: entries.submissionScreenshotPath,
  teamScreenshot: entries.teamScreenshotPath,
  entryScreenshot: entries.screenshotPath,
  icon: content.iconPath,
  teamExport: team.exportPath,
  csv: exports.csvPath,
  projects: exports.projectsPath,
  archive: exports.archivePath,
  siteUpload: sites.uploadPath,
  assistant: assistant.askPath,
  oauthStart: auth.oauthStart,
};

export type { EventRow, Need, Role, Stage, Todos } from "./hack";
export type { EventDetail, LeavePreview } from "./event";
export type { EntryCard, Submission } from "./entries";
export type { Me } from "./me";
