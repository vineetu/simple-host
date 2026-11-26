// The one entry summary card (the old ui.entryCard): title, team, track, and
// the five summary rows, each saying "Not filled in yet" when empty. Used by
// Submissions, and by the judge, organiser and entry screens as they are rebuilt.
import type { ReactNode } from "react";
import { Badge } from "@kit/components/ui/badge";
import { cn } from "@kit/lib/utils";
import type { EntryCard as Entry } from "@/lib/client";
import { AI_TOOLS, MATURITY } from "@/lib/labels";

const AI = AI_TOOLS.map((t) => t.toLowerCase());

function Row({ label, children }: { label: string; children?: ReactNode }) {
  return (
    <>
      <dt className="text-sm text-ink-soft">{label}</dt>
      <dd className={cn("m-0 min-w-0", !children && "text-ink-faint italic")}>{children || "Not filled in yet"}</dd>
    </>
  );
}

export function EntryCard({ entry, compact, badges, children }: { entry: Entry; compact?: boolean; badges?: ReactNode; children?: ReactNode }) {
  const e = entry;
  const tools = (e.tools || []).filter(Boolean);
  const m = e.maturity ? MATURITY[e.maturity] : undefined;
  const H = compact ? "h3" : "h2";
  return (
    <article className="flex min-w-0 flex-col rounded-wobble border border-rule bg-card px-4 py-4 md:px-5">
      <div className="flex flex-wrap items-start gap-2">
        <div className="min-w-0 flex-1">
          <H className="type-section">{e.title || "Untitled"}</H>
          {e.team_name && (
            <p className="mt-0.5 text-sm text-ink-soft">
              {e.team_name}
              {e.team_members ? " · " + e.team_members : ""}
            </p>
          )}
        </div>
        {(e.track?.name || badges) && (
          <div className="flex flex-wrap gap-1.5">
            {e.track?.name && <Badge>{e.track.name} track</Badge>}
            {badges}
          </div>
        )}
      </div>
      <dl className="mt-3 grid grid-cols-1 gap-x-4 gap-y-1 md:grid-cols-[8rem_minmax(0,1fr)] md:gap-y-2">
        <Row label="Problem">{e.problem}</Row>
        <Row label="Solution">{e.tagline}</Row>
        <Row label="For">{e.audience}</Row>
        <Row label="Tools & AI">
          {tools.length > 0 && (
            <ul className="m-0 flex list-none flex-wrap gap-1.5 p-0">
              {tools.map((t) => (
                <li key={t}>
                  <Badge tone={AI.includes(t.toLowerCase()) ? "accent" : "neutral"}>{t}</Badge>
                </li>
              ))}
            </ul>
          )}
        </Row>
        <Row label="Works today">
          {m && (
            <span>
              <Badge tone={e.maturity === "working" ? "ok" : "neutral"}>{m.label}</Badge> <span className="text-sm text-ink-soft">{m.note}</span>
            </span>
          )}
        </Row>
      </dl>
      {children}
    </article>
  );
}
