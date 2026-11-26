import { cn } from "@kit/lib/utils";
import { initials, markColours } from "@/lib/labels";

/** Wordmark is "simple·hack": teal simple, red ·hack, in the hand font (the only place it is used). */
export function Wordmark({ className }: { className?: string }) {
  return (
    <span className={cn("font-wordmark text-[26px] leading-tight font-[700] text-coral", className)}>
      simple<span className="text-wordmark">·hack</span>
    </span>
  );
}

/** EventMark is an event's initials on its own colour (the organiser picks the colour). */
export function EventMark({ title, color, size = "md" }: { title: string; color?: string; size?: "sm" | "md" }) {
  const ok = /^#[0-9a-f]{6}$/i.test(color || "");
  const { bg, ink } = markColours(ok ? color! : "#d15d48");
  return (
    <span
      aria-hidden="true"
      className={cn("grid flex-none place-items-center rounded-sm text-sm", size === "md" ? "size-8" : "size-6 text-xs")}
      style={{ background: bg, color: ink }}
    >
      {initials(title)}
    </span>
  );
}
