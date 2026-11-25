import type { ComponentProps } from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "@/lib/utils";

// A status word on a row: live, taken down, admin.
const badgeVariants = cva("inline-block rounded-tag border px-2 text-xs leading-normal font-normal whitespace-nowrap", {
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

/** Chips is a row of filter chips; exactly one is pressed. */
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
    <div role="group" aria-label={label} className="my-2 flex flex-wrap gap-2">
      {options.map(([v, text]) => (
        <button
          key={v}
          type="button"
          aria-pressed={v === value}
          onClick={() => onChange(v)}
          className={cn(
            "min-h-11 cursor-pointer rounded-wobble border-[1.5px] px-3.5 font-note text-note whitespace-nowrap shadow-paper-sm",
            v === value ? "border-accent bg-accent text-on-accent" : "border-rule bg-card text-ink-soft hover:text-ink",
          )}
        >
          {text}
        </button>
      ))}
    </div>
  );
}
