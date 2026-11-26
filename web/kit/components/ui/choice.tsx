import type { ReactNode } from "react";
import { RadioGroup as R } from "radix-ui";
import { cn } from "@kit/lib/utils";

/** Check is a checkbox with its words, the whole row a 44 px target. */
export function Check({
  checked,
  onChange,
  children,
  hint,
  disabled,
}: {
  checked: boolean;
  onChange: (on: boolean) => void;
  children: ReactNode;
  hint?: ReactNode;
  disabled?: boolean;
}) {
  return (
    <label className={cn("relative flex min-h-11 cursor-pointer items-start gap-3 py-2.5", disabled && "cursor-default opacity-60")}>
      {/* The real checkbox covers the whole row (the tap target); the box beside the words is drawn. */}
      <input
        type="checkbox"
        className="peer absolute inset-0 size-full cursor-pointer appearance-none rounded-field"
        checked={checked}
        disabled={disabled}
        onChange={(e) => onChange(e.target.checked)}
      />
      <span
        aria-hidden="true"
        className="mt-0.5 grid size-5 flex-none place-items-center rounded-sm border-[1.5px] border-ink-soft bg-card text-on-accent peer-checked:border-accent peer-checked:bg-accent"
      >
        {checked && (
          <svg viewBox="0 0 16 16" className="size-3.5" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round">
            <path d="M3.5 8.5l3 3 6-7" />
          </svg>
        )}
      </span>
      <span className="min-w-0">
        <span className="block">{children}</span>
        {hint && <span className="mt-0.5 block text-sm text-ink-soft">{hint}</span>}
      </span>
    </label>
  );
}

/**
 * RadioCards is one choice from a few, each a card with a title and a line
 * saying what it means (Radix: arrow keys move, one tab stop). The chosen
 * card can show more under it (children of the option).
 */
export function RadioCards<V extends string>({
  label,
  value,
  onChange,
  options,
}: {
  label: string;
  value: V;
  onChange: (v: V) => void;
  options: { value: V; title: string; hint: ReactNode; more?: ReactNode }[];
}) {
  return (
    <R.Root aria-label={label} value={value} onValueChange={(v) => onChange(v as V)} className="grid gap-2.5">
      {options.map((o) => {
        const on = o.value === value;
        return (
          <div
            key={o.value}
            className={cn("rounded-field border-[1.5px] bg-card", on ? "border-accent shadow-paper-sm" : "border-rule")}
          >
            {/* The radio covers the whole head of the card (the tap target); its dot is drawn. */}
            <label className="relative flex min-h-11 cursor-pointer items-start gap-3 px-4 py-3">
              <R.Item value={o.value} aria-label={o.title} aria-describedby={label.replace(/\W+/g, "-") + "-" + o.value} className="absolute inset-0 cursor-pointer rounded-field" />
              <span
                aria-hidden="true"
                className={cn("mt-0.5 grid size-5 flex-none place-items-center rounded-tag border-[1.5px] bg-card", on ? "border-accent" : "border-ink-soft")}
              >
                {on && <span className="size-2.5 rounded-tag bg-accent" />}
              </span>
              <span className="min-w-0">
                <span className="block font-semibold">{o.title}</span>
                <span id={label.replace(/\W+/g, "-") + "-" + o.value} className="mt-0.5 block text-sm text-ink-soft">
                  {o.hint}
                </span>
              </span>
            </label>
            {on && o.more && <div className="border-t border-rule px-4 pt-3 pb-4">{o.more}</div>}
          </div>
        );
      })}
    </R.Root>
  );
}
