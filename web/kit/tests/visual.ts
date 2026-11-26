// The catalogue checks as Playwright tests: every entry at phone and desktop
// width, with no console errors, no sideways scroll, no clipped text, 44 px
// tap targets, no bold on a phone, axe clean, and the same picture as the
// committed baseline (accept new ones with the app's visual:accept).
import { expect, test } from "@playwright/test";
import { axe, fitViewport, layout } from "./checks";

export function catalogueTests(entries: { id: string; title: string }[], now: Date) {
  for (const e of entries) {
    for (const width of [390, 1280]) {
      test(`${e.id} @${width}`, async ({ page }) => {
        const errors: string[] = [];
        page.on("console", (m) => m.type() === "error" && errors.push(m.text()));
        page.on("pageerror", (err) => errors.push(err.message));
        await page.clock.setFixedTime(now);
        await page.setViewportSize({ width, height: 800 });
        await page.goto(`/catalogue.html?id=${e.id}`);
        await page.waitForSelector("body[data-ready]");
        const problems = [...errors.map((x) => "console: " + x), ...(await layout(page)), ...(await axe(page))];
        expect(problems, e.title).toEqual([]);
        await fitViewport(page);
        await expect(page).toHaveScreenshot(`${e.id}-${width}.png`);
      });
    }
  }
}
