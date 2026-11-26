// The tokens contract: Simple Hack's token file names exactly the tokens the
// owner app's does (the kit's primitives use these names), only the values differ.
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { expect, it } from "vitest";

const names = (file: string) =>
  [...readFileSync(join(import.meta.dirname, file), "utf8").replace(/\/\*[\s\S]*?\*\//g, "").matchAll(/(--[a-z0-9-]+)\s*:/g)].map((m) => m[1]).sort();

it("names the same tokens as the owner app", () => {
  expect(names("styles/tokens.css")).toEqual(names("../../host/src/styles/tokens.css"));
});
