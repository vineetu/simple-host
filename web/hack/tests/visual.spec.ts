// Every catalogue entry at 390 and 1280 px (the kit's checks; baselines in tests/baseline).
import { expect, test } from "@playwright/test";
import { axe, fitViewport, layout } from "../../kit/tests/checks";
import { catalogueTests } from "../../kit/tests/visual";
import { pageEntries } from "../src/pages/catalogue";
import { NOW } from "../src/catalogue/fixtures";
import { entries } from "./entries";

catalogueTests(entries, NOW);

// The pages the server writes (src/pages/catalogue.ts): opened as they are,
// at the catalogue's clock, with the same checks.
for (const e of pageEntries) {
  for (const width of [390, 1280]) {
    test(`${e.id} @${width}`, async ({ page }) => {
      test.setTimeout(120_000); // the features page is long; axe takes a while on it
      const errors: string[] = [];
      page.on("console", (m) => m.type() === "error" && !e.allow?.test(m.text()) && errors.push(m.text()));
      page.on("pageerror", (err) => errors.push(err.message));
      await page.clock.install({ time: NOW }); // the films' and countdown's timers stay still
      await page.setViewportSize({ width, height: 800 });
      await page.goto(e.page, { waitUntil: "load" });
      await page.evaluate(() => document.fonts.ready);
      const problems = [...errors.map((x) => "console: " + x), ...(await layout(page)), ...(await axe(page))];
      expect(problems, e.title).toEqual([]);
      // A film shows its poster in the picture, not whichever frame it reached.
      await page.evaluate(() =>
        document.querySelectorAll("video").forEach((v) => {
          v.pause();
          v.removeAttribute("src");
          v.querySelectorAll("source").forEach((s) => s.remove());
          v.load();
        }),
      );
      await fitViewport(page);
      await expect(page).toHaveScreenshot(`${e.id}-${width}.png`, { timeout: 30_000 });
    });
  }
}
