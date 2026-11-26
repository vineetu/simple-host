// The catalogue entries, read the way the catalogue reads them (every
// screens/<area>/catalogue.ts), for the visual checks.
import { existsSync, readdirSync } from "node:fs";
import { join } from "node:path";
import type { Entry } from "../../kit/catalogue/harness";

const dir = join(import.meta.dirname, "../src/screens");
export const entries: Entry[] = [];
for (const area of readdirSync(dir).sort()) {
  const f = join(dir, area, "catalogue.ts");
  if (existsSync(f)) entries.push(...((await import(f)) as { entries: Entry[] }).entries);
}
