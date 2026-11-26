import { cn } from "@kit/lib/utils";

/** Meter is how much of something is used, as a bar with its words beside it. */
export function Meter({ label, value, max, text, warnAt = 0.9 }: { label: string; value: number; max: number; text: string; warnAt?: number }) {
  const share = max > 0 ? Math.min(1, value / max) : 0;
  const warn = share >= warnAt;
  return (
    <div>
      <div
        role="meter"
        aria-label={label}
        aria-valuemin={0}
        aria-valuemax={max}
        aria-valuenow={value}
        aria-valuetext={text}
        className="h-2 overflow-hidden rounded-tag bg-paper-lo"
      >
        <div className={cn("h-full rounded-tag", warn ? "bg-bad" : "bg-accent")} style={{ width: Math.max(share * 100, value > 0 ? 1.5 : 0) + "%" }} />
      </div>
      <p className={cn("mt-1.5 text-sm", warn ? "font-medium text-bad" : "text-ink-soft")}>{text}</p>
    </div>
  );
}
