import type { ComponentProps } from "react";
import { cn } from "@/lib/utils";

/** Card is the paper card with the hand-drawn edge. */
export function Card({ className, ...props }: ComponentProps<"section">) {
  return (
    <section
      data-slot="card"
      className={cn(
        "w-full rounded-wobble border-[1.5px] border-ink-soft bg-card px-6 pt-7 pb-6 text-ink shadow-paper md:px-9 md:pt-9 md:pb-7",
        className,
      )}
      {...props}
    />
  );
}

export function CardTitle({ className, ...props }: ComponentProps<"h1">) {
  return <h1 className={cn("mb-1.5 type-card", className)} {...props} />;
}

export function CardLead({ className, ...props }: ComponentProps<"p">) {
  return <p className={cn("mb-5 text-ink-soft", className)} {...props} />;
}

/** Fine is small print under a form. */
export function Fine({ className, ...props }: ComponentProps<"p">) {
  return <p className={cn("mt-4 text-sm text-ink-faint", className)} {...props} />;
}
