// Every catalogue entry at phone and desktop width: no console errors, no
// sideways scroll, no clipped text, 44 px tap targets, axe clean, and the
// same picture as the committed baseline (tests/baseline; accept new ones with
// `npm run visual:accept`).
import { expect, test } from "@playwright/test";
import { entries } from "../src/catalogue/entries";
import { NOW } from "../src/catalogue/fixtures";
import { pageEntries } from "../src/catalogue/pages";
import { axe, layout } from "./checks";

for (const e of entries) {
  for (const width of [390, 1280]) {
    test(`${e.id} @${width}`, async ({ page }) => {
      const errors: string[] = [];
      page.on("console", (m) => m.type() === "error" && errors.push(m.text()));
      page.on("pageerror", (err) => errors.push(err.message));
      await page.clock.setFixedTime(NOW);
      await page.setViewportSize({ width, height: 800 });
      await page.goto(`/catalogue.html?id=${e.id}`);
      await page.waitForSelector("body[data-ready]");
      const problems = [...errors.map((x) => "console: " + x), ...(await layout(page)), ...(await axe(page))];
      expect(problems, e.title).toEqual([]);
      await expect(page).toHaveScreenshot(`${e.id}-${width}.png`, { fullPage: true });
    });
  }
}

// The pages the server writes: opened as they are, with no fixtures.
for (const e of pageEntries) {
  for (const width of [390, 1280]) {
    test(`${e.id} @${width}`, async ({ page }) => {
      const errors: string[] = [];
      page.on("console", (m) => m.type() === "error" && errors.push(m.text()));
      page.on("pageerror", (err) => errors.push(err.message));
      await page.setViewportSize({ width, height: 800 });
      await page.goto(e.page, { waitUntil: "load" });
      await page.evaluate(() => document.fonts.ready);
      const problems = [...errors.map((x) => "console: " + x), ...(await layout(page)), ...(await axe(page))];
      expect(problems, e.title).toEqual([]);
      await expect(page).toHaveScreenshot(`${e.id}-${width}.png`, { fullPage: true });
    });
  }
}
