import type { ReactNode } from "react";
import { cn } from "@/lib/utils";

/** Wordmark is "simple-host": coral simple, teal host, in the hand font. */
export function Wordmark({ className }: { className?: string }) {
  return (
    <span className={cn("font-wordmark text-[29px] leading-tight font-bold text-wordmark", className)}>
      <span className="text-coral">simple</span>
      <span className="px-[.03em] text-ink-faint">-</span>host
    </span>
  );
}

const navLink = "inline-flex min-h-11 min-w-11 justify-center items-center px-1 text-sm font-medium text-ink-soft no-underline hover:text-ink md:text-base";

/**
 * PageShell is every owner-app page: the header (wordmark, Home, Get started,
 * Docs, and one link on the right), the page, and the footer links.
 */
export function PageShell({
  title,
  children,
  right,
  center,
}: {
  /** The browser tab's title, before " · Simple Host". */
  title: string;
  children: ReactNode;
  /** The header's right-hand link, e.g. Your sites. */
  right?: { href: string; label: string };
  /** A single card in the middle of the page (sign-in). */
  center?: boolean;
}) {
  return (
    <>
      <title>{title + " · Simple Host"}</title>
      <header className="border-b-[1.5px] border-dashed border-ink-soft">
        <div className="mx-auto flex max-w-page items-center gap-2 px-4 py-2.5 md:gap-4 md:px-6">
          <a href="/" className="mr-auto inline-flex min-h-11 items-center no-underline" aria-label="simple-host home">
            <Wordmark className="text-2xl md:text-[29px]" />
          </a>
          <nav aria-label="Main" className="flex items-center gap-x-2 md:gap-x-4">
            <a className={cn(navLink, "hidden md:inline-flex")} href="/">
              Home
            </a>
            <a className={navLink} href="/install">
              Get started
            </a>
            <a className={navLink} href="/docs">
              Docs
            </a>
          </nav>
          {right && (
            <a
              href={right.href}
              className="inline-flex min-h-11 items-center rounded-wobble border-[1.5px] border-ink-soft px-3 text-sm font-medium text-ink no-underline hover:text-accent md:text-base"
            >
              {right.label}
            </a>
          )}
        </div>
      </header>
      <main
        className={cn(
          "flex-1",
          center
            ? "flex items-start justify-center px-4 pt-10 pb-12 md:items-center md:pt-14 md:pb-18"
            : "mx-auto w-full max-w-page px-4 md:px-6",
        )}
      >
        {children}
      </main>
      <footer className="mt-12 border-t-[1.5px] border-dashed border-ink-soft">
        <nav aria-label="More" className="mx-auto flex max-w-page flex-wrap gap-x-5 px-4 pt-4 pb-8 md:px-6">
          {[
            ["/docs", "Docs"],
            ["/privacy", "Privacy"],
            ["/terms", "Terms"],
            ["/support", "Support"],
          ].map(([href, label]) => (
            <a key={href} href={href} className="inline-flex min-h-11 min-w-11 items-center justify-center text-sm text-ink-soft no-underline hover:text-ink">
              {label}
            </a>
          ))}
        </nav>
      </footer>
    </>
  );
}

/** PageTitle is a page's heading line, with an optional line under it. */
export function PageTitle({ children, sub }: { children: ReactNode; sub?: ReactNode }) {
  return (
    <div className="mt-8">
      <h1 className="type-page">{children}</h1>
      {sub && <p className="mt-0.5 text-sm text-ink-soft">{sub}</p>}
    </div>
  );
}

/** Section is a titled part of a page; count is the small number after the title. */
export function Section({
  title,
  count,
  note,
  children,
  className,
  hideTitle,
}: {
  title: string;
  count?: ReactNode;
  note?: ReactNode;
  children: ReactNode;
  className?: string;
  /** The title is for screen readers only. */
  hideTitle?: boolean;
}) {
  return (
    <section className={cn("mt-7 first:mt-1", className)} aria-label={hideTitle ? title : undefined}>
      {!hideTitle && <h2 className="mb-1 flex flex-wrap items-baseline gap-2 type-section">
        {title}
        {count != null && <span className="text-sm font-normal text-ink-soft">{count}</span>}
      </h2>}
      {note && <p className="mt-1 mb-2.5 max-w-[80ch] text-sm text-ink-soft">{note}</p>}
      {children}
    </section>
  );
}
