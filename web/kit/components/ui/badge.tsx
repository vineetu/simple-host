import type { ComponentProps } from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "@kit/lib/utils";

// A status word on a row: live, taken down, admin.
const badgeVariants = cva("inline-block rounded-tag border px-2 text-xs leading-normal font-medium whitespace-nowrap", {
  variants: {
    tone: {
      neutral: "border-ink-soft text-ink-soft",
      ok: "border-good text-good",
      down: "border-bad text-bad",
      accent: "border-accent text-accent",
    },
  },
  defaultVariants: { tone: "neutral" },
});

export function Badge({ className, tone, ...props }: ComponentProps<"span"> & VariantProps<typeof badgeVariants>) {
  return <span className={cn(badgeVariants({ tone }), className)} {...props} />;
}

/**
 * Chips is a row of filter chips; exactly one is pressed. It never wraps: on a
 * narrow screen the row scrolls sideways, running to the screen edges.
 */
export function Chips<V extends string>({
  label,
  options,
  value,
  onChange,
}: {
  label: string;
  options: readonly (readonly [V, string])[];
  value: V;
  onChange: (v: V) => void;
}) {
  return (
    <div
      role="group"
      aria-label={label}
      data-scroll-x
      className="-mx-4 my-2 flex gap-2 overflow-x-auto px-4 [scrollbar-width:none] md:mx-0 md:flex-wrap md:overflow-visible md:px-0 [&::-webkit-scrollbar]:hidden"
    >
      {options.map(([v, text]) => (
        <button
          key={v}
          type="button"
          aria-pressed={v === value}
          onClick={() => onChange(v)}
          className={cn(
            "min-h-11 flex-none cursor-pointer rounded-tag border-[1.5px] px-4 text-sm font-medium whitespace-nowrap",
            v === value ? "border-accent bg-accent text-on-accent" : "border-rule bg-card text-ink-soft hover:text-ink",
          )}
        >
          {text}
        </button>
      ))}
    </div>
  );
}
