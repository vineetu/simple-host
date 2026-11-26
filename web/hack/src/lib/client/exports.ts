// Downloads: fetched with bytes (they need the key), so these are paths.
import type { Call } from "@kit/api/call";
import { E, type Untyped } from "./_e";

export type CsvExport = "participants" | "teams" | "entries" | "scores" | "results";
export const csvPath = (slug: string, what: CsvExport) => E(slug, "export", what + ".csv");
export const projectsPath = (slug: string) => E(slug, "export", "projects.tar.gz");
export const projectsLink = (call: Call, slug: string) => call<Untyped>("POST", E(slug, "export", "projects-link"));
export const ownTeamLink = (call: Call, slug: string) => call<Untyped>("POST", E(slug, "export", "own-team-link"));
export const archivePath = () => "/v1/hack/archive";
