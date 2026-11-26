import { defineConfig } from "@playwright/test";

// SH_CATALOGUE_PORT: another port for the catalogue server (two checkouts at once).
const PORT = process.env.SH_CATALOGUE_PORT || "4319";

// Two kinds of browser checks:
//   catalogue  every catalogue entry at 390 and 1280: console errors, sideways
//              scroll, clipped text, small tap targets, axe, and the baseline picture
//   real       the real service (started by tests/real.spec.ts) clicked through
export default defineConfig({
  testDir: "tests",
  timeout: 30_000,
  fullyParallel: true,
  workers: 4,
  reporter: [["line"]],
  snapshotPathTemplate: "tests/baseline/{arg}{ext}",
  expect: { toHaveScreenshot: { maxDiffPixelRatio: 0.002, threshold: 0.2, animations: "disabled", caret: "hide" } },
  use: {
    launchOptions: {
      executablePath: process.env.SH_CHROMIUM || "/home/ubuntu/.cache/ms-playwright/chromium-1234/chrome-linux/chrome",
    },
  },
  projects: [
    { name: "catalogue", testMatch: "visual.spec.ts", use: { baseURL: `http://127.0.0.1:${PORT}` } },
    { name: "real", testMatch: "real.spec.ts", workers: 1 },
  ],
  webServer: {
    command: `npx vite preview --mode catalogue --port ${PORT} --strictPort --host 127.0.0.1`,
    url: `http://127.0.0.1:${PORT}/catalogue.html`,
    reuseExistingServer: false,
  },
});
