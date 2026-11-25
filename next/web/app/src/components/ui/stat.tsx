import type { ReactNode } from "react";

/** Stats is a row of key numbers on the teal wash, as on the old admin page. */
export function Stats({ children }: { children: ReactNode }) {
  return (
    <div className="my-1 grid grid-cols-[repeat(auto-fill,minmax(140px,1fr))] gap-x-5 rounded-tab border border-dashed border-ink-soft bg-accent-soft px-3.5 py-2.5">
      {children}
    </div>
  );
}

export function Stat({ value, label }: { value: ReactNode; label: ReactNode }) {
  return (
    <div className="min-w-0 py-1.5">
      <div className="text-2xl leading-tight font-bold tabular-nums">{value}</div>
      <div className="font-note text-note text-ink-soft">{label}</div>
    </div>
  );
}
