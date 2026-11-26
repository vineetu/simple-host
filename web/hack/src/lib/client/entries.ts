// Entries: the entry card, rules, the gallery of submissions.
import type { Call } from "@kit/api/call";
import { E, seg, type Untyped } from "./_e";

/** One entry's summary card, as GET E/submissions and the entry views give it. */
export type EntryCard = {
  title?: string;
  tagline?: string;
  problem?: string;
  audience?: string;
  tools?: string[];
  maturity?: "working" | "partial" | "concept" | "";
  track?: { name?: string } | null;
  video_url?: string;
  video_host?: string;
  code_url?: string;
  code_host?: string;
  site_url?: string;
  has_screenshot?: boolean;
  team_name?: string;
  team_members?: string;
};
export type Submission = EntryCard & { team: string; team_name: string; screenshot_url?: string };

export const get = (call: Call, slug: string) => call<Untyped>("GET", E(slug, "entry"));
export const save = (call: Call, slug: string, body: Untyped) => call<Untyped>("PUT", E(slug, "entry"), body);
export const submit = (call: Call, slug: string) => call<Untyped>("POST", E(slug, "entry", "submit"));
export const unsubmit = (call: Call, slug: string) => call<Untyped>("POST", E(slug, "entry", "unsubmit"));
/** The screenshot's bytes: GET/PUT/DELETE at this path (bytes, not JSON). */
export const screenshotPath = (slug: string) => E(slug, "entry", "screenshot");
export const removeScreenshot = (call: Call, slug: string) => call<Untyped>("DELETE", E(slug, "entry", "screenshot"));
export const rules = (call: Call, slug: string) => call<Untyped>("GET", E(slug, "entry-rules"));
export const setRules = (call: Call, slug: string, body: Untyped, dryRun = false) => call<Untyped>("PUT", E(slug, "entry-rules") + (dryRun ? "?dry_run=1" : ""), body);
export const list = (call: Call, slug: string) => call<Untyped>("GET", E(slug, "entries"));
export const exclude = (call: Call, slug: string, team: string, on: boolean) => call<Untyped>("POST", E(slug, "teams", seg(team), "entry", on ? "exclude" : "include"));
export const teamScreenshotPath = (slug: string, team: string) => E(slug, "teams", seg(team), "screenshot");
export const submissions = async (call: Call, slug: string) => (await call<{ submissions?: Submission[] }>("GET", E(slug, "submissions"))).submissions || [];
export const submissionScreenshotPath = (slug: string, team: string) => E(slug, "submissions", seg(team), "screenshot");
export const usage = (call: Call, slug: string) => call<Untyped>("GET", E(slug, "usage"));
