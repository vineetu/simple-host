import { fileURLToPath, URL } from "node:url";
import { defineConfig } from "vitest/config";

// Unit tests for the owner app's plain logic and the shared kit's (formatting, searching, filtering).
const at = (p: string) => fileURLToPath(new URL(p, import.meta.url));
export default defineConfig({
  root: at(".."),
  resolve: { alias: { "@kit": at("../kit"), "@": at("./src") } },
  test: { include: ["host/src/**/*.test.ts", "kit/**/*.test.ts"] },
});
