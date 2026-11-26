// The signed-in frame, shared by the apps: a side rail from 700 px, a top bar
// and a bottom bar on a phone. Everything product-specific comes in as props:
// the wordmark, the rail's groups of links (with badges and a reason when a
// link is not open yet), what sits at the rail's foot, and which links the
// phone's bottom bar carries. Links the bottom bar has no room for go in its
// "More" sheet, so every link is reachable at every width.
// (Simple Host's owner app still uses its own web/host/src/app-shell/AppShell.tsx,
// which keeps its built output byte-identical; it moves onto this one when it next changes.)
import { useEffect, useState, type ReactNode } from "react";
import { Link, useLocation } from "react-router";
import { Dialog as D } from "radix-ui";
import { cn } from "@kit/lib/utils";

export type ShellLink = {
  href: string;
  label: string;
  icon: ReactNode;
  /** Shown as the page the person is on. */
  current?: boolean;
  /** A count beside the label (0 or unset shows nothing). */
  badge?: number;
  /** Why the link is not open yet; the link is then shown greyed and does nothing. */
  closed?: string;
  /** A full page load (a page another app serves). */
  reload?: boolean;
  /** On the phone's bottom bar (the rest go in More). */
  bar?: boolean;
  /** A button, not a link (Sign out). */
  onSelect?: () => void;
};

export type ShellGroup = { label?: string; head?: ReactNode; links: ShellLink[] };

export type ShellProps = {
  /** The browser tab's title, the suffix included. */
  title: string;
  brand: { href: string; label: string; wordmark: ReactNode; under?: ReactNode };
  groups: ShellGroup[];
  foot: ShellLink[];
  /** Beside the wordmark on a phone (an event switcher). */
  phoneTop?: ReactNode;
  footerLinks?: [string, string][];
  children: ReactNode;
};

const railLink =
  "flex min-h-11 items-center gap-3 rounded-tab px-3 text-base font-medium text-ink-soft no-underline hover:bg-paper-lo hover:text-ink";

function Count({ n }: { n?: number }) {
  if (!n) return null;
  return (
    <span className="ml-auto min-w-6 rounded-tag bg-accent px-1.5 text-center text-xs leading-5 text-on-accent">
      {n}
      <span className="sr-only"> waiting</span>
    </span>
  );
}

function RailItem({ it, onDone }: { it: ShellLink; onDone?: () => void }) {
  const inner = (
    <>
      {it.icon}
      <span className="min-w-0 flex-1">{it.label}</span>
      <Count n={it.badge} />
    </>
  );
  if (it.closed)
    return (
      <span className={cn(railLink, "cursor-default text-ink-faint hover:bg-transparent hover:text-ink-faint")} aria-disabled="true" title={it.closed}>
        {inner}
        <span className="sr-only">: {it.closed}</span>
      </span>
    );
  if (it.onSelect)
    return (
      <button type="button" onClick={() => (onDone?.(), it.onSelect!())} className={cn(railLink, "w-full cursor-pointer text-left")}>
        {inner}
      </button>
    );
  return (
    <Link
      to={it.href}
      reloadDocument={it.reload}
      onClick={onDone}
      aria-current={it.current ? "page" : undefined}
      className={cn(railLink, it.current && "bg-accent-soft text-ink")}
    >
      {inner}
    </Link>
  );
}

function Groups({ groups, onDone }: { groups: ShellGroup[]; onDone?: () => void }) {
  return (
    <>
      {groups.map((g, i) => (
        <div key={i} className={cn(i > 0 && "mt-3 border-t border-rule pt-3")}>
          {g.head}
          <nav aria-label={g.label || "Main"} className="flex flex-col gap-1">
            {g.links.map((it) => (
              <RailItem key={it.href + it.label} it={it} onDone={onDone} />
            ))}
          </nav>
        </div>
      ))}
    </>
  );
}

const MoreIcon = () => (
  <svg viewBox="0 0 20 20" width="20" height="20" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" aria-hidden="true">
    <path d="M5 10h.01M10 10h.01M15 10h.01" />
  </svg>
);

export function AppShell({ title, brand, groups, foot, phoneTop, footerLinks = [], children }: ShellProps) {
  const { pathname, search } = useLocation();
  const [more, setMore] = useState(false);
  useEffect(() => setMore(false), [pathname, search]);
  // Tells what floats over the page (the toast) how much room the bars take.
  useEffect(() => {
    document.documentElement.dataset.shell = "app";
    return () => void delete document.documentElement.dataset.shell;
  }, []);
  const all = groups.flatMap((g) => g.links);
  const bar = all.filter((it) => it.bar).slice(0, 4);
  const rest = all.some((it) => !bar.includes(it)) || foot.length > 0;

  return (
    <div className="min-h-dvh md:grid md:grid-cols-[248px_minmax(0,1fr)]">
      <title>{title}</title>
      {/* Wide: the side rail. */}
      <aside className="sticky top-0 hidden h-dvh flex-col overflow-y-auto border-r border-rule bg-paper-lo/40 px-3 py-4 md:flex">
        <a href={brand.href} className="mb-4 inline-flex min-h-11 flex-col justify-center px-3 no-underline">
          {brand.wordmark}
          <span className="sr-only">: {brand.label}</span>
          {brand.under && <span className="mt-0.5 truncate text-sm text-ink-soft">{brand.under}</span>}
        </a>
        <Groups groups={groups} />
        <div className="mt-auto border-t border-rule pt-3">
          <nav aria-label="You" className="flex flex-col gap-1">
            {foot.map((it) => (
              <RailItem key={it.href + it.label} it={it} />
            ))}
          </nav>
        </div>
      </aside>

      {/* Phone: a top bar with the wordmark. */}
      <header className="flex min-w-0 items-center gap-2 border-b border-rule px-4 py-1.5 md:hidden">
        <a href={brand.href} className="inline-flex min-h-11 flex-none items-center no-underline">
          {brand.wordmark}
          <span className="sr-only">: {brand.label}</span>
        </a>
        {phoneTop && <div className="ml-auto min-w-0">{phoneTop}</div>}
      </header>

      <div className="flex min-w-0 flex-col">
        <main className="mx-auto w-full max-w-page flex-1 px-4 pb-8 md:px-8">{children}</main>
        <footer className="mx-auto w-full max-w-page px-4 pb-24 md:px-8 md:pb-6">
          {footerLinks.length > 0 && (
            <nav aria-label="About" className="flex flex-wrap gap-x-5 border-t border-rule pt-2">
              {footerLinks.map(([href, label]) => (
                <a key={href} href={href} className="inline-flex min-h-11 min-w-11 items-center justify-center text-sm text-ink-soft no-underline hover:text-ink">
                  {label}
                </a>
              ))}
            </nav>
          )}
        </footer>
      </div>

      {/* Phone: the bottom bar, and More for everything else. */}
      <nav
        aria-label="Main"
        className="fixed inset-x-0 bottom-0 z-30 grid auto-cols-fr grid-flow-col border-t border-rule bg-card pb-[env(safe-area-inset-bottom)] md:hidden"
      >
        {bar.map((it) =>
          it.closed ? (
            <span key={it.href} aria-disabled="true" title={it.closed} className="flex min-h-14 flex-col items-center justify-center gap-0.5 text-xs text-ink-faint">
              {it.icon}
              {it.label}
              <span className="sr-only">: {it.closed}</span>
            </span>
          ) : (
            <Link
              key={it.href}
              to={it.href}
              reloadDocument={it.reload}
              aria-current={it.current ? "page" : undefined}
              className={cn("relative flex min-h-14 flex-col items-center justify-center gap-0.5 text-xs text-ink-soft no-underline", it.current && "text-accent")}
            >
              {it.icon}
              {it.label}
              {!!it.badge && (
                <span className="absolute top-1 right-[calc(50%-22px)] min-w-5 rounded-tag bg-accent px-1 text-center text-xs leading-5 text-on-accent">
                  {it.badge}
                  <span className="sr-only"> waiting</span>
                </span>
              )}
            </Link>
          ),
        )}
        {rest && (
          <D.Root open={more} onOpenChange={setMore}>
            <D.Trigger className="flex min-h-14 cursor-pointer flex-col items-center justify-center gap-0.5 text-xs text-ink-soft">
              <MoreIcon />
              More
            </D.Trigger>
            <D.Portal>
              <D.Overlay className="fixed inset-0 z-40 bg-ink/40" />
              <D.Content className="fixed inset-x-0 bottom-0 z-50 max-h-[85dvh] overflow-y-auto rounded-t-[14px] border-t border-rule bg-card px-3 pt-3 pb-[calc(env(safe-area-inset-bottom)+12px)] shadow-pop">
                <D.Title className="px-3 pb-2 type-section">More</D.Title>
                <D.Description className="sr-only">Every page of this app</D.Description>
                <Groups groups={groups} onDone={() => setMore(false)} />
                <nav aria-label="You" className="mt-3 flex flex-col gap-1 border-t border-rule pt-3">
                  {foot.map((it) => (
                    <RailItem key={it.href + it.label} it={it} onDone={() => setMore(false)} />
                  ))}
                </nav>
                <D.Close className="mt-2 min-h-11 w-full cursor-pointer rounded-field border border-rule text-base text-ink">Close</D.Close>
              </D.Content>
            </D.Portal>
          </D.Root>
        )}
      </nav>
    </div>
  );
}
