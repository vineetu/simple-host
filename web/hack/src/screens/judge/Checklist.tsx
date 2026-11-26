// The judge's checklist card (the old checklist.js renderChecklist, judge
// side: no Not needed there) and the one-press acknowledgement row the
// Rubric and My teams screens add (bindAcknowledgement).
import { useState, type ReactNode } from "react";
import { Button } from "@kit/components/ui/button";
import { Section } from "@kit/components/ui/page-shell";
import { useToast } from "@kit/components/ui/toast";
import { cn } from "@kit/lib/utils";
import type { ApiError } from "@kit/api/call";
import { useHack, type EventDetail } from "@/lib/client";
import { navigation } from "@/navigation.gen";
import { AppLink } from "@/app/nav";
import { actionLabel, stepDetail, type Checklist, type ChecklistItem } from "./data";
import { useJudgeChecklist, useRefreshJudging } from "./queries";

const targets = navigation.targets as readonly { id: string; action_label?: string | null }[];

function Steps({ items, list, start, filled }: { items: ChecklistItem[]; list: Checklist; start: number; filled: boolean }) {
  return (
    <ol className="m-0 list-none p-0">
      {items.map((item, k) => {
        const done = item.state === "done";
        const opted = item.state === "not_needed";
        const now = !done && list.next?.item_id === item.id;
        const detail = stepDetail(item);
        return (
          <li key={item.id} data-checklist-id={item.id} className={cn("flex min-h-14 items-center gap-3 border-b border-rule py-2 last:border-b-0")}>
            <span
              aria-hidden="true"
              className={cn(
                "grid size-7 flex-none place-items-center rounded-tag border-[1.5px] text-sm",
                done ? "border-good bg-good text-on-accent" : now ? "border-accent text-accent" : "border-rule text-ink-soft",
              )}
            >
              {done ? "✓" : start + k + 1}
            </span>
            <span className="min-w-0 flex-1">
              <span className={cn("block", now && "font-semibold")}>
                {item.label}
                {done && <span className="sr-only"> (done)</span>}
              </span>
              {detail && <span className="block text-sm text-ink-soft">{detail}</span>}
            </span>
            {!done && !opted && item.actionable && item.link?.href && (
              <Button variant={now && filled ? "primary" : "secondary"} asChild className="flex-none">
                <AppLink to={item.link.href}>{actionLabel(item, targets)}</AppLink>
              </Button>
            )}
          </li>
        );
      })}
    </ol>
  );
}

const GROUPS: [string, string][] = [
  ["earlier_required", "Earlier required"],
  ["current", ""],
  ["earlier_optional", "Earlier items"],
  ["history", "History"],
];

/** JudgeChecklist is "Your checklist": the server's items, the current ones open, earlier ones folded. */
export function JudgeChecklist({ list, primary }: { list: Checklist; primary: boolean }) {
  const items = list.items || [];
  let n = 0;
  const parts: ReactNode[] = [];
  for (const [group, label] of GROUPS) {
    const rows = items.filter((i) => (i.group || "current") === group);
    if (!rows.length) continue;
    // Only one filled button on a screen: the next step's is filled only when no hero above has one.
    const steps = <Steps key={group} items={rows} list={list} start={n} filled={primary} />;
    n += rows.length;
    parts.push(
      label ? (
        <details key={group} open={group === "earlier_required" && rows.some((i) => i.actionable && i.state !== "done")} className="mt-2">
          <summary className="flex min-h-11 cursor-pointer items-center text-ink-soft">{label}</summary>
          {steps}
        </details>
      ) : (
        steps
      ),
    );
  }
  return (
    <Section title="Your checklist" className="rounded-wobble border border-rule bg-card px-4 py-3">
      {parts}
    </Section>
  );
}

/** writeLock is why nothing on this page can change, or "" (ui.js writeLockReason). */
export function writeLock(d: EventDetail): string {
  if (d.admin_view) return "You are viewing as a platform admin. Nothing on this page can be changed.";
  if (d.event.taken_down) return "This event has been taken down. Nothing on this page can be changed.";
  if (d.event.stage === "archived") return "This event has ended. Nothing on this page changes any more.";
  return "";
}

/**
 * Acknowledge is a line saying what pressing records, and the button that
 * records it (POST E/checklist/acknowledgements/{id}, with the rubric
 * version on screen). It is left out when the item is already done for this
 * version, or the checklist does not have it.
 */
export function Acknowledge({
  d,
  id,
  label,
  version,
  help = "",
  doneText = "",
  onStale,
}: {
  d: EventDetail;
  id: "j.rubric" | "j.conflicts";
  label: string;
  version?: string;
  help?: string;
  doneText?: string;
  onStale?: () => void;
}) {
  const api = useHack();
  const toast = useToast();
  const refresh = useRefreshJudging(d.event.slug);
  const q = useJudgeChecklist(d.event.slug);
  const [busy, setBusy] = useState(false);
  const [saved, setSaved] = useState(false);
  if (id !== "j.conflicts" && !version) return null;
  if (!q.data) return null;
  const item = q.data.items?.find((i) => i.id === id);
  if (!saved && (!item || (item.state === "done" && (!version || item.version === version)) || (!version && !item.version && id !== "j.conflicts"))) return null;
  const lock = writeLock(d);
  const press = async () => {
    setBusy(true);
    try {
      await api.judging.acknowledge(d.event.slug, id, version ?? item?.version);
      setSaved(true);
      if (!doneText) toast("Saved");
      void refresh();
    } catch (e) {
      const err = e as ApiError;
      toast(err.message || "Cannot reach the server.", { bad: true });
      if (err.code === "stale_checklist_version") onStale?.();
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="mt-4 flex flex-wrap items-center justify-end gap-x-4 gap-y-2 border-t border-rule pt-3">
      <span className="min-w-0 flex-[1_1_14rem] text-sm text-ink-soft" role="status">
        {saved && doneText ? doneText : help}
      </span>
      {!(saved && doneText) &&
        (saved ? (
          <span className="text-sm text-good">Done</span>
        ) : (
          <Button disabled={busy || !!lock} title={lock || undefined} onClick={press}>
            {label}
          </Button>
        ))}
    </div>
  );
}
