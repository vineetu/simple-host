// Small pieces the judge screens share.
import { useEffect, useState, type ReactNode } from "react";
import { useBytes } from "@/app/Providers";
import { EmptyState } from "@kit/components/ui/empty-state";
import { Button } from "@kit/components/ui/button";
import { cn } from "@kit/lib/utils";
import { href } from "@/routes/resolve";
import { AppLink } from "@/app/nav";

/** Progress is "n of m scored" as a bar. */
export function Progress({ done, total, label, tour }: { done: number; total: number; label: string; tour?: boolean }) {
  const pct = total ? Math.round((done / total) * 100) : 0;
  return (
    <div
      role="progressbar"
      aria-label={label}
      aria-valuenow={done}
      aria-valuemin={0}
      aria-valuemax={total}
      data-tour={tour ? "progress" : undefined}
      className="my-2 h-2 overflow-hidden rounded-tag bg-paper-lo"
    >
      <div className="h-full rounded-tag bg-accent" style={{ width: pct + "%" }} />
    </div>
  );
}

/** NotOpen is "Scoring not open", said once, with the way back. */
export function NotOpen({ slug, back }: { slug: string; back?: "home" | "queue" }) {
  return (
    <EmptyState
      title="Scoring not open"
      action={
        back ? (
          <Button asChild>
            <AppLink to={href("judge", slug, back)}>{back === "home" ? "Back to Overview" : "Back to My teams"}</AppLink>
          </Button>
        ) : undefined
      }
    >
      Scoring opens when the organiser starts judging.
    </EmptyState>
  );
}

/** NewTab is a link out to something a team typed in, its host named. */
export function NewTab({ to, children, primary, className }: { to: string; children: ReactNode; primary?: boolean; className?: string }) {
  return (
    <Button variant={primary ? "primary" : "secondary"} asChild className={cn("max-w-full", className)}>
      <a href={to} target="_blank" rel="noopener noreferrer">
        <span className="min-w-0 [overflow-wrap:anywhere]">{children}</span> <span aria-hidden="true">↗</span>
        <span className="sr-only"> (opens in a new tab)</span>
      </a>
    </Button>
  );
}

/**
 * Shot is the team's screenshot, read with the key as soon as the screen
 * shows (the judge's one evidence when the site cannot be framed, so it is
 * not left to load on scroll). Nothing shows if it fails.
 */
export function Shot({ path, alt }: { path: string; alt: string }) {
  const bytes = useBytes();
  const [src, setSrc] = useState("");
  const [gone, setGone] = useState(false);
  useEffect(() => {
    let live = true;
    bytes(path).then(
      (b) => {
        const r = new FileReader();
        r.onload = () => live && typeof r.result === "string" && setSrc(r.result);
        r.onerror = () => live && setGone(true);
        r.readAsDataURL(b);
      },
      () => live && setGone(true),
    );
    return () => {
      live = false;
    };
  }, [bytes, path]);
  if (gone) return null;
  return (
    <div className="mt-3 aspect-[16/10] overflow-hidden rounded-field border border-rule bg-paper-lo">
      {src && <img src={src} alt={alt} className="block size-full object-cover object-top" />}
    </div>
  );
}
