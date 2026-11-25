import { dayLabel, num } from "@/lib/format";

/**
 * Bars is a count per day as columns, with the first and last day under it.
 * Screen readers get the total in the label; each bar has its day and count
 * as a tooltip for pointers.
 */
export function Bars({ days, label }: { days: { day: string; n: number }[]; label: string }) {
  const max = Math.max(1, ...days.map((d) => d.n));
  return (
    <figure className="my-3">
      <div role="img" aria-label={label} className="flex h-24 items-end gap-[2px] border-b border-rule">
        {days.map((d) => (
          <span
            key={d.day}
            title={dayLabel(d.day) + ": " + num(d.n)}
            className="min-w-0 flex-1 rounded-t-[2px] bg-accent"
            style={{ height: d.n ? Math.max(3, (d.n / max) * 100) + "%" : "1px", opacity: d.n ? 1 : 0.35 }}
          />
        ))}
      </div>
      {days.length > 1 && (
        <figcaption className="mt-1 flex justify-between text-xs text-ink-soft" aria-hidden="true">
          <span>{dayLabel(days[0].day)}</span>
          <span>{dayLabel(days[days.length - 1].day)}</span>
        </figcaption>
      )}
    </figure>
  );
}
