import { fileURLToPath, URL } from "node:url";
import { defineConfig } from "vitest/config";

// Unit tests for Simple Hack's plain logic (routing, labels, the client) and the shared kit's.
const at = (p: string) => fileURLToPath(new URL(p, import.meta.url));
export default defineConfig({
  root: at(".."),
  resolve: { alias: { "@kit": at("../kit"), "@": at("./src") } },
  test: { include: ["hack/src/**/*.test.ts", "kit/**/*.test.ts"] },
});
