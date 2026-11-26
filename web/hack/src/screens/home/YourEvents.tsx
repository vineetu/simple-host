// Your events (/events): the person's home, one level above any event. What
// waits on them across events (Needs you), their current events with a plain
// next step and a row menu (Open event, Leave), past events folded away, and
// the other starting points: Find a hackathon, Account, Create event.
import { useEffect } from "react";
import { useLocation } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { Button } from "@kit/components/ui/button";
import { EmptyState } from "@kit/components/ui/empty-state";
import { Menu, type MenuItem } from "@kit/components/ui/menu";
import { PageTitle, Section } from "@kit/components/ui/page-shell";
import { Skeleton } from "@kit/components/ui/skeleton";
import { useToast } from "@kit/components/ui/toast";
import { useHack, type EventRow } from "@/lib/client";
import { eventGroups } from "@/lib/events";
import { roleLabel, stageLabel } from "@/lib/labels";
import { takeFlash } from "@/lib/session";
import { eventHome, needHref } from "@/routes/resolve";
import { useEvents } from "@/app/data";
import { useLeave } from "@/app/Leave";
import { EventMark } from "@/app/marks";
import { AppLink, useGo } from "@/app/nav";
import { Failed } from "@/app/states";
import { leaveItem, nextAction } from "./data";

const rowBox = "flex min-h-14 items-center gap-3 border-b border-rule px-1 py-2 last:border-b-0";

function Row({ e, onLeave }: { e: EventRow; onLeave: (e: EventRow) => void }) {
  const go = useGo();
  const blocked = e.taken_down && e.role !== "organiser";
  const next = nextAction(e);
  const label = (
    <span className="min-w-0 flex-1">
      <span className="block font-semibold text-ink">{e.title}</span>
      <span className="block text-sm text-ink-soft">
        {roleLabel(e.role)} · {e.taken_down ? "Taken down" : stageLabel(e.stage)}
      </span>
      {next && <span className="block text-sm text-ink md:hidden">{next}</span>}
    </span>
  );
  if (blocked)
    return (
      <li className={rowBox}>
        <EventMark title={e.title} color={e.color} />
        {label}
      </li>
    );
  const leave = leaveItem(e);
  const items: MenuItem[] = [
    { label: "Open event", onSelect: () => go(eventHome(e.role, e.slug)) },
    { separator: true },
    leave.disabled ? { label: leave.label, disabled: true } : { label: leave.label, onSelect: () => onLeave(e), danger: true },
  ];
  return (
    <li className={rowBox}>
      <AppLink to={eventHome(e.role, e.slug)} className="flex min-h-11 min-w-0 flex-1 items-center gap-3 no-underline">
        <EventMark title={e.title} color={e.color} />
        {label}
        {next && <span className="hidden flex-none text-sm text-ink-soft md:inline">{next}</span>}
      </AppLink>
      <Menu label={"More for " + e.title} items={items} />
    </li>
  );
}

function List({ label, events, onLeave }: { label: string; events: EventRow[]; onLeave: (e: EventRow) => void }) {
  return (
    <ul aria-label={label} className="m-0 list-none rounded-wobble border border-rule bg-card px-3 py-1">
      {events.map((e) => (
        <Row key={e.slug} e={e} onLeave={onLeave} />
      ))}
    </ul>
  );
}

function NeedsYou() {
  const api = useHack();
  // Left out quietly when it fails: the event rows still work.
  const q = useQuery({ queryKey: ["todos"], queryFn: () => api.hack.todos() });
  const rows = (q.data?.events || []).flatMap((e) => (e.items || []).map((item, i) => ({ e, item, key: e.slug + i })));
  if (!rows.length) return null;
  return (
    <Section title="Needs you">
      <ul aria-label="Needs you across events" className="m-0 list-none rounded-wobble border border-rule bg-card px-3 py-1">
        {rows.map(({ e, item, key }) => (
          <li key={key} className="border-b border-rule last:border-b-0">
            <AppLink to={needHref(e.role, e.slug, item)} className="flex min-h-14 flex-col justify-center px-1 py-2 no-underline">
              <span className="font-semibold text-ink">{e.title}</span>
              <span className="text-sm text-ink-soft">{item.text}</span>
            </AppLink>
          </li>
        ))}
      </ul>
    </Section>
  );
}

export default function YourEvents() {
  const q = useEvents();
  const toast = useToast();
  const { search } = useLocation();
  const [openLeave, dialog] = useLeave();
  useEffect(() => {
    const text = takeFlash();
    if (text) toast(text);
  }, [toast]);

  if (q.isPending)
    return (
      <>
        <PageTitle>Your events</PageTitle>
        <div className="mt-7" role="status" aria-busy="true" aria-label="Loading your events">
          <Skeleton className="h-16 w-full" />
          <Skeleton className="mt-2 h-16 w-full" />
          <Skeleton className="mt-2 h-16 w-full" />
        </div>
      </>
    );
  if (q.isError)
    return (
      <>
        <PageTitle>Your events</PageTitle>
        <Failed error={q.error} retry={() => q.refetch()} />
      </>
    );

  const events = q.data;
  const { current, past } = eventGroups(events);
  const leave = (e: EventRow) => openLeave(e.slug, e.title);
  if (!events.length)
    return (
      <>
        <PageTitle>Your events</PageTitle>
        <EmptyState
          title="You’re not part of any event yet"
          action={
            <div className="flex flex-wrap justify-center gap-2.5">
              <Button variant="primary" asChild>
                <AppLink to="/events/new">Create event</AppLink>
              </Button>
              <Button asChild>
                <a href="/directory">Find a hackathon</a>
              </Button>
            </div>
          }
        >
          Have a join or judge link? Open it to get started.
        </EmptyState>
      </>
    );

  return (
    <>
      <PageTitle>Your events</PageTitle>
      <NeedsYou />
      <Section title="Current events" hideTitle={false}>
        {current.length ? <List label="Current events" events={current} onLeave={leave} /> : <p className="text-ink-soft">Nothing is on right now. Your past events are below.</p>}
      </Section>
      {past.length > 0 && (
        <details open={new URLSearchParams(search).get("past") === "1"} className="mt-6">
          <summary className="flex min-h-11 cursor-pointer items-center type-section">Past events ({past.length})</summary>
          <List label="Past events" events={past} onLeave={leave} />
        </details>
      )}
      <div className="mt-7 flex flex-wrap gap-2.5">
        <Button asChild>
          <a href="/directory">Find a hackathon</a>
        </Button>
        <Button asChild>
          <AppLink to="/account">Account</AppLink>
        </Button>
      </div>
      <div className="mt-8 border-t border-rule pt-5">
        <Button variant="primary" asChild>
          <AppLink to="/events/new">Create event</AppLink>
        </Button>
      </div>
      {dialog}
    </>
  );
}
