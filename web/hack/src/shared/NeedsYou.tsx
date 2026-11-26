// The one "Needs you" card on an event's home (the old needs.js): each thing
// waiting on the person, the server's first sentence as its title and the
// rest (or where it goes) under it, a link into the person's own area, and
// Dismiss for the ones that can be dismissed. Participant Overview and judge
// Overview use it. (Your events has its own list across events.)
import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Button } from "@kit/components/ui/button";
import { Section } from "@kit/components/ui/page-shell";
import { useToast } from "@kit/components/ui/toast";
import type { ApiError } from "@kit/api/call";
import { useHack, type Need, type Role } from "@/lib/client";
import { needHref } from "@/routes/resolve";
import { AppLink } from "@/app/nav";

const OPEN: Record<string, string> = { team: "Open Team", website: "Open Website", entry: "Open Entry", people: "Review applications", home: "", queue: "Open My teams" };

/** splitNeed is a Needs you line's title and the short line under it (needs.js). */
export function splitNeed(item: { kind: string; text: string; section?: string }): [string, string] {
  const text = String(item.text || "");
  if (item.kind === "team_message") return [text, OPEN[item.section || ""] || ""];
  const i = text.indexOf(". ");
  if (i > 0) return [text.slice(0, i + 1), text.slice(i + 2)];
  return [text, OPEN[item.section || ""] || ""];
}

export function NeedsYou({ role, slug, items }: { role: Role; slug: string; items: Need[] }) {
  const api = useHack();
  const qc = useQueryClient();
  const toast = useToast();
  const [busy, setBusy] = useState("");
  if (!items.length) return null;
  const dismiss = async (id: string) => {
    setBusy(id);
    try {
      await api.event.noticesSeen(slug, [id]);
      await qc.invalidateQueries({ queryKey: ["event", slug] });
    } catch (e) {
      if ((e as ApiError).status !== 401) toast((e as Error).message || "Could not dismiss that. Try again.", { bad: true });
    } finally {
      setBusy("");
    }
  };
  return (
    <Section title="Needs you">
      <ul aria-label="Needs you" className="m-0 list-none rounded-wobble border border-rule bg-card px-3 py-1">
        {items.map((item, i) => {
          const [title, sub] = splitNeed(item);
          return (
            <li key={item.id || i} className="flex min-h-14 items-center gap-2 border-b border-rule last:border-b-0">
              <AppLink to={needHref(role, slug, item)} className="flex min-h-11 min-w-0 flex-1 flex-col justify-center px-1 py-2 no-underline">
                <span className="font-semibold text-ink">{title}</span>
                {sub && <span className="text-sm text-ink-soft">{sub}</span>}
              </AppLink>
              {item.dismissable && item.id && (
                <Button aria-label={"Dismiss: " + title} disabled={busy === item.id} onClick={() => dismiss(item.id!)}>
                  Dismiss
                </Button>
              )}
            </li>
          );
        })}
      </ul>
    </Section>
  );
}
