// Small pieces the dashboard pages share.
import type { ReactNode } from "react";
import { Badge } from "@kit/components/ui/badge";
import { ago, dateOf } from "@kit/lib/format";
import { cn } from "@kit/lib/utils";
import type { SiteItem } from "./data";

/** SiteState is a site's one status word: taken down, offline or live. */
export function SiteState({ s }: { s: Pick<SiteItem, "offline" | "taken_down"> }) {
  if (s.taken_down) return <Badge tone="down">taken down</Badge>;
  if (s.offline) return <Badge>offline</Badge>;
  return <Badge tone="ok">live</Badge>;
}

/** Ago is "3 days ago", with the date as its tooltip. */
export function Ago({ at }: { at: string | null | undefined }) {
  if (!at) return <span className="text-ink-soft">-</span>;
  return (
    <time dateTime={at} title={dateOf(at)} className="whitespace-nowrap">
      {ago(at)}
    </time>
  );
}

/** Part is a titled block of a page: a heading, a line saying what it is for, then its controls. */
export function Part({ title, note, children, className, danger }: { title: string; note?: ReactNode; children?: ReactNode; className?: string; danger?: boolean }) {
  return (
    <section
      className={cn(
        "mt-8 max-w-[720px] first:mt-2",
        danger && "rounded-field border-[1.5px] border-bad/60 px-4 pt-4 pb-5 md:px-5",
        className,
      )}
    >
      <h2 className={cn("type-section", danger && "text-bad")}>{title}</h2>
      {note && <p className="mt-1 mb-3 max-w-[70ch] text-sm text-ink-soft">{note}</p>}
      {!note && <div className="mb-3" />}
      {children}
    </section>
  );
}

/** Row is one line of a short list: words on the left, controls on the right. */
export function Row({ children, end }: { children: ReactNode; end?: ReactNode }) {
  return (
    <li className="flex flex-wrap items-center gap-x-4 gap-y-2 border-b border-rule py-2.5 first:border-t">
      <div className="min-w-0 flex-[1_1_10rem]">{children}</div>
      {end && <div className="flex flex-none flex-wrap items-center gap-2">{end}</div>}
    </li>
  );
}

export const External = () => (
  <svg viewBox="0 0 16 16" aria-hidden="true" className="size-3.5 flex-none" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
    <path d="M9 3h4v4M13 3L7.5 8.5M11.5 9.5V13H3V4.5h3.5" />
  </svg>
);
