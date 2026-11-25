import type { ReactNode } from "react";

/** EmptyState says why a list or page has nothing in it, and what to do. */
export function EmptyState({ title, children, action }: { title: string; children?: ReactNode; action?: ReactNode }) {
  return (
    <div className="mx-auto my-6 max-w-prose rounded-wobble border-[1.5px] border-dashed border-ink-soft px-6 py-6 text-center">
      <p className="font-hand text-2xl font-bold">{title}</p>
      {children && <div className="mt-1 text-ink-soft">{children}</div>}
      {action && <div className="mt-4 flex justify-center">{action}</div>}
    </div>
  );
}
