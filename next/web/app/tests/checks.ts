// The checks every screen must pass, at every width.
import type { Page } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";

export type Problem = string;

/** layout finds sideways scroll, text cut off or pushed off screen, and tap targets under 44 px. */
export async function layout(page: Page): Promise<Problem[]> {
  return page.evaluate(() => {
    const out: string[] = [];
    const vw = document.documentElement.clientWidth;
    if (document.documentElement.scrollWidth > vw + 1) out.push(`sideways scroll: page is ${document.documentElement.scrollWidth}px wide in ${vw}px`);
    const name = (el: Element) => {
      const t = (el.getAttribute("aria-label") || el.textContent || "").trim().replace(/\s+/g, " ").slice(0, 40);
      return `<${el.tagName.toLowerCase()}${el.id ? "#" + el.id : ""}> "${t}"`;
    };
    const shown = (el: Element) => {
      const r = el.getBoundingClientRect();
      // 1 px boxes are screen-reader-only text (Radix's VisuallyHidden).
      return r.width > 1 && r.height > 1 && getComputedStyle(el).visibility !== "hidden";
    };
    for (const el of document.body.querySelectorAll("*")) {
      if (!shown(el) || el.closest("svg") || el.closest(".sr-only")) continue;
      const cs = getComputedStyle(el);
      const r = el.getBoundingClientRect();
      if (r.right > vw + 1 && cs.position !== "fixed") out.push(`off screen to the right: ${name(el)} ends at ${Math.round(r.right)}px`);
      const clips = /(hidden|clip)/.test(cs.overflowX) || cs.textOverflow === "ellipsis";
      if (clips && el.scrollWidth > el.clientWidth + 1 && el.textContent?.trim()) out.push(`text cut off: ${name(el)}`);
    }
    const tappable = "a[href], button, input:not([type=hidden]), select, textarea, [role=tab], [role=menuitem], [tabindex]:not([tabindex='-1'])";
    for (const el of document.body.querySelectorAll(tappable)) {
      if (!shown(el) || el.closest(".sr-only")) continue;
      // A link or button inside a sentence is exempt (WCAG 2.5.8 "inline").
      const p = el.parentElement;
      const own = (el.textContent || "").trim().length;
      if (getComputedStyle(el).display === "inline" || (p && /^(P|LI|SPAN|TD|DIV)$/.test(p.tagName) && (p.textContent || "").trim().length > own + 15 && !el.closest("nav")))
        continue;
      const r = el.getBoundingClientRect();
      if (r.height < 43.5 || r.width < 43.5) out.push(`tap target ${Math.round(r.width)}x${Math.round(r.height)}: ${name(el)}`);
    }
    return out;
  });
}

/** axe runs the WCAG 2.1 A and AA rules. */
export async function axe(page: Page): Promise<Problem[]> {
  const res = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"]).analyze();
  return res.violations.flatMap((v) => v.nodes.map((n) => `axe ${v.id}: ${v.help} at ${n.target.join(" ")}`));
}
