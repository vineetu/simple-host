import type { ComponentProps } from "react";
import { cn } from "@kit/lib/utils";

/** Skeleton holds the place of content that is loading, at its final size, so nothing jumps. */
export function Skeleton({ className, ...props }: ComponentProps<"div">) {
  return <div aria-hidden="true" className={cn("rounded-field bg-paper-lo motion-safe:animate-pulse", className)} {...props} />;
}
