import { defineConfig } from "vitest/config";

// Unit tests for the screens' plain logic (formatting, searching, sorting).
export default defineConfig({ test: { include: ["src/**/*.test.ts"] } });
