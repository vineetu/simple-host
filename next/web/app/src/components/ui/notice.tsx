import type { ReactNode } from "react";
import { cn } from "@/lib/utils";

/**
 * Notice is a line about the state of the thing on screen (offline, taken
 * down, an invitation), with what to do about it. tone "bad" is for what
 * stops visitors; it carries an icon as well as the colour.
 */
export function Notice({ tone = "info", title, children, action }: { tone?: "info" | "bad"; title: string; children?: ReactNode; action?: ReactNode }) {
  return (
    <div
      role={tone === "bad" ? "alert" : "status"}
      className={cn(
        "my-4 flex flex-wrap items-center gap-x-4 gap-y-2 rounded-field border-[1.5px] px-4 py-3",
        tone === "bad" ? "border-bad bg-card" : "border-accent bg-accent-soft",
      )}
    >
      <div className="min-w-0 flex-[1_1_16rem]">
        <p className={cn("font-semibold", tone === "bad" && "text-bad")}>
          {tone === "bad" && <span aria-hidden="true">⚠ </span>}
          {title}
        </p>
        {children && <div className="mt-0.5 text-sm text-ink-soft">{children}</div>}
      </div>
      {action && <div className="flex flex-none flex-wrap gap-2">{action}</div>}
    </div>
  );
}
