// The one titled box a screen's sections sit in (a card with a heading and a
// line under it). Team, Website and the judge screens use it; tone gives the
// judge's border colours.
import type { ReactNode } from "react";
import { cn } from "@kit/lib/utils";

export function Box({
  title,
  sub,
  tone,
  children,
  className,
  ...rest
}: { title?: string; sub?: ReactNode; tone?: "warn" | "ok" | "accent"; children?: ReactNode; className?: string } & Record<`data-${string}`, string | undefined>) {
  return (
    <section
      {...rest}
      className={cn(
        "mt-5 rounded-wobble border bg-card px-4 py-4 md:px-5",
        tone === "warn" ? "border-ink-soft" : tone === "ok" ? "border-good" : tone === "accent" ? "border-[1.5px] border-accent" : "border-rule",
        className,
      )}
    >
      {title && <h2 className="type-section">{title}</h2>}
      {sub && <p className="mt-0.5 text-sm text-ink-soft">{sub}</p>}
      {children}
    </section>
  );
}
