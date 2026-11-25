import type { ComponentProps, ReactNode } from "react";
import { cn } from "@/lib/utils";

// Text inputs are 16 px (no zoom on a phone) and at least 44 px tall.
export function Input({ className, ...props }: ComponentProps<"input">) {
  return (
    <input
      data-slot="input"
      className={cn(
        "block h-12 w-full min-w-0 rounded-field border-[1.5px] border-ink-soft bg-paper px-3.5 font-sans text-base text-ink placeholder:text-ink-faint",
        "focus-visible:border-accent aria-invalid:border-bad",
        className,
      )}
      {...props}
    />
  );
}

export function Label({ className, ...props }: ComponentProps<"label">) {
  return <label className={cn("mb-1.5 block text-sm font-semibold text-ink", className)} {...props} />;
}

/** NativeSelect is the browser's own select, in the input style. */
export function NativeSelect({ className, ...props }: ComponentProps<"select">) {
  return (
    <select
      className={cn(
        "h-11 max-w-full min-w-0 cursor-pointer rounded-field border-[1.5px] border-ink-soft bg-card px-3 font-sans text-base text-ink focus-visible:border-accent",
        className,
      )}
      {...props}
    />
  );
}

const icons = {
  bad: (
    <svg viewBox="0 0 16 16" aria-hidden="true" className="mt-0.5 size-4 flex-none">
      <circle cx="8" cy="8" r="7" fill="none" stroke="currentColor" strokeWidth="1.6" />
      <path d="M8 4.2v4.6M8 10.9v.9" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" />
    </svg>
  ),
  good: (
    <svg viewBox="0 0 16 16" aria-hidden="true" className="mt-0.5 size-4 flex-none">
      <circle cx="8" cy="8" r="7" fill="none" stroke="currentColor" strokeWidth="1.6" />
      <path d="M4.8 8.3l2.2 2.2 4.2-4.6" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  ),
};

/** FieldMessage is the line under a field: progress, an error (bad) or good news. */
export function FieldMessage({ tone, id, children }: { tone?: "bad" | "good"; id?: string; children: ReactNode }) {
  if (!children) return <p id={id} role="status" className="sr-only" />;
  return (
    <p
      id={id}
      role={tone === "bad" ? "alert" : "status"}
      className={cn(
        "mt-2 flex items-start gap-2 text-sm",
        tone === "bad" ? "text-bad" : tone === "good" ? "text-good" : "text-ink-soft",
      )}
    >
      {tone && icons[tone]}
      <span>{children}</span>
    </p>
  );
}
