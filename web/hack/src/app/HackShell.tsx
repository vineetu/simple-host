// Simple Hack's signed-in frame on the kit's AppShell: the wordmark leads
// Home (Your events); the rail lists the person's events with the open
// event's sections under it (from navigation.json, per role, with badges and
// stage rules), and Account, Sign out and Help at the foot. On a phone the
// bottom bar carries the role's phone tabs and More carries the rest.
import { useState, type ReactNode } from "react";
import { AppShell, type ShellGroup, type ShellLink } from "@kit/app-shell/AppShell";
import { Dialog, DialogActions } from "@kit/components/ui/dialog";
import { Button } from "@kit/components/ui/button";
import { Badge } from "@kit/components/ui/badge";
import { dateTime, nextDeadline } from "@/lib/time";
import { navigation } from "@/navigation.gen";
import { useHack, type EventDetail, type EventRow, type Need, type Role } from "@/lib/client";
import { eventGroups } from "@/lib/events";
import { roleLabel, stageLabel } from "@/lib/labels";
import { forget } from "@/lib/session";
import { eventHome, href } from "@/routes/resolve";
import { useEvents, useMe } from "./data";
import { Icon } from "./icons";
import { EventMark, Wordmark } from "./marks";
import { inApp } from "./nav";

type Where = { screen: string; event?: EventDetail; section?: string; params?: URLSearchParams };

const needCount = (items: Need[], kinds: string[]) => items.filter((n) => kinds.includes(n.kind)).reduce((s, n) => s + (n.count || 1), 0);

/** unreadAnnouncements: posted since this browser last opened Announcements (localStorage, per event; needs.js). */
export function unreadAnnouncements(slug: string, times: string[] | undefined): number {
  if (!Array.isArray(times) || !times.length) return 0;
  let seen: string | null = null;
  try {
    seen = localStorage.getItem("sh_ann_seen:" + slug);
  } catch {
    return 0;
  }
  if (!seen) return times.length;
  const at = Date.parse(seen);
  return times.filter((t) => Date.parse(t) > at).length;
}

function badgeFor(role: Role, id: string, d: EventDetail, section?: string): number {
  const needs = d.needs_you || [];
  if (role === "participant" && id === "team") return needCount(needs, ["join_request", "invite"]) || (d.me?.team?.admin && d.me.team.pending_requests) || 0;
  if (role === "participant" && id === "home") return needs.filter((n) => n.dismissable).length;
  if (role === "participant" && id === "announcements") return section === "announcements" ? 0 : unreadAnnouncements(d.event.slug, d.event.announcement_times);
  if (role === "organiser" && id === "people") return needCount(needs, ["application_new"]);
  if (role === "organiser" && id === "teams" && ["open", "building"].includes(d.event.stage)) return d.organiser?.counts?.on_no_team || 0;
  return 0;
}

/** eventSections are the role's links for this event at its stage (shell.js mount). */
export function eventSections(role: Role, d: EventDetail, section?: string): ShellLink[] {
  const ev = d.event;
  const nav = navigation.roles.find((r) => r.id === role)?.nav || [];
  const stage = ev.stage === "archived" ? "ended" : ev.stage;
  const links: ShellLink[] = nav
    .filter((it) => {
      const stages = "stages" in it ? (it.stages as readonly string[]) : undefined;
      return !stages || ((it.id !== "results" || ev.results_published) && stages.includes(stage));
    })
    .map((it) => {
      const to = href(role, ev.slug, it.id);
      const pending = d.me?.approval_status && d.me.approval_status !== "approved" && role === "participant";
      return {
        href: to,
        label: it.label,
        icon: <Icon name={it.icon} />,
        current: section === it.id,
        badge: badgeFor(role, it.id, d, section),
        closed:
          role === "participant" && ["entry", "website"].includes(it.id) && !d.me?.team
            ? "Join or start a team first"
            : pending && section !== it.id
              ? "Waiting for the organisers"
              : undefined,
        reload: !inApp(to),
        bar: !!it.phone_tab,
      };
    });
  if (role === "judge" && d.can?.manage) {
    const to = href("organiser", ev.slug, "overview");
    links.push({ href: to, label: "Manage event", icon: <Icon name="settings" />, reload: !inApp(to) });
  }
  return links;
}

function rowLabel(e: EventRow) {
  return (
    roleLabel(e.role) +
    " · " +
    (e.taken_down ? "Taken down" : e.approval_status === "pending" ? "Waiting for approval" : e.approval_status === "rejected" ? "Not accepted" : stageLabel(e.stage))
  );
}

function EventHead({ e, open }: { e: EventRow; open: boolean }) {
  const content = (
    <>
      <EventMark title={e.title} color={e.color} />
      <span className="min-w-0 flex-1">
        <span className={open ? "block truncate font-semibold text-ink" : "block truncate"} title={e.title}>
          {e.title}
        </span>
        <span className="block truncate text-sm text-ink-soft" title={rowLabel(e)}>
          {rowLabel(e)}
        </span>
      </span>
    </>
  );
  const cls = "flex min-h-11 items-center gap-3 rounded-tab px-3 py-1 text-base text-ink-soft no-underline hover:bg-paper-lo hover:text-ink";
  if (e.taken_down && e.role !== "organiser") return <div className={cls}>{content}</div>;
  const to = eventHome(e.role, e.slug);
  return (
    <a href={to} className={cls}>
      {content}
    </a>
  );
}

/** Help's walkthrough link: back into the screen that owns the role's tour (shell.js). */
function walkthroughHref(role: Role, d: EventDetail, section?: string, params?: URLSearchParams): string {
  if (role === "participant" && d.me?.approval_status && d.me.approval_status !== "approved") return "";
  const scoringOpen = !!(d.event.scoring_open && !d.event.judging_locked_at);
  const s = role === "organiser" ? "overview" : role === "judge" ? (section === "scoring" && scoringOpen ? "scoring" : "home") : section === "team" && !d.me?.team ? "team" : "home";
  const team = s === "scoring" && params?.get("team") ? "&team=" + encodeURIComponent(params.get("team")!) : "";
  return href(role, d.event.slug, s) + "&walkthrough=1" + team;
}

export function HackShell({ title, where, children }: { title: string; where: Where; children: ReactNode }) {
  const me = useMe().data;
  const events = useEvents().data || [];
  const api = useHack();
  const [warn, setWarn] = useState(false);
  const d = where.event;
  const role = d?.role;
  const slug = d?.event.slug;
  const { current, past, pastCount } = eventGroups(events, slug);

  const home: ShellGroup = {
    label: "Home",
    links: [
      { href: "/events", label: "Your events", icon: <Icon name="home" />, current: where.screen === "landing-your-events", reload: !inApp("/events"), bar: !d },
      { href: "/directory", label: "Find a hackathon", icon: <Icon name="globe" />, reload: true, bar: !d },
    ],
  };
  const groups: ShellGroup[] = [home];
  if (d && role) {
    const mine = events.find((e) => e.slug === slug) || ({ slug: slug!, title: d.event.title, stage: d.event.stage, role, color: d.event.color } as EventRow);
    groups.push({
      label: d.event.title,
      head: (
        <div className="mb-1">
          <EventHead e={{ ...mine, role: d.admin_view ? "organiser" : mine.role }} open />
          {d.admin_view && <p className="px-3 text-sm text-ink-soft">Read-only inspection</p>}
        </div>
      ),
      links: eventSections(role, d, where.section),
    });
  }
  const others = current.filter((e) => e.slug !== slug).slice(0, d ? 4 : 5);
  if (others.length || pastCount)
    groups.push({
      label: "Your events",
      head: (
        <div className="flex flex-col gap-1">
          {others.map((e) => (
            <EventHead key={e.slug} e={e} open={false} />
          ))}
        </div>
      ),
      links: [
        { href: "/events", label: `All events (${events.length})`, icon: <Icon name="events" />, reload: !inApp("/events") },
        ...(past.length ? [{ href: "/events?past=1", label: `Past events (${pastCount})`, icon: <Icon name="calendar" />, reload: !inApp("/events") }] : []),
      ],
    });

  const signOut = async () => {
    let failed = false;
    try {
      await api.auth.signOut();
    } catch (e) {
      failed = (e as { status?: number }).status !== 400 && (e as { status?: number }).status !== 401;
    }
    forget();
    if (failed) setWarn(true);
    else location.replace("/");
  };
  const support = (document.body.getAttribute("data-support-contact") || "").trim();
  const replay = d && role ? walkthroughHref(role, d, where.section, where.params) : "";
  const foot: ShellLink[] = [
    { href: "/account", label: "Account", icon: <Icon name="user" />, current: where.screen === "account", reload: !inApp("/account") },
    { href: "#sign-out", label: "Sign out", icon: <Icon name="signout" />, onSelect: signOut },
    { href: "/get-started", label: "Get started", icon: <Icon name="help" />, reload: true },
    ...(replay ? [{ href: replay, label: "Replay walkthrough", icon: <Icon name="help" />, reload: true }] : []),
    ...(support.includes("@") ? [{ href: "mailto:" + support, label: "Contact support", icon: <Icon name="help" />, reload: true }] : []),
  ];
  const personName = me?.display_name || me?.login || me?.username || me?.email || "";

  return (
    <>
      <AppShell
        title={title + " · Simple Hack"}
        brand={{ href: "/events", label: "Your events", wordmark: <Wordmark />, under: personName }}
        groups={groups}
        foot={foot}
        phoneTop={d ? <span className="block truncate text-right text-sm text-ink-soft">{d.event.title}</span> : undefined}
        footerLinks={[
          ["/privacy.html", "Privacy"],
          ["/terms", "Terms"],
        ]}
      >
        {d && <StatusStrip d={d} />}
        {children}
      </AppShell>
      <Dialog open={warn} onOpenChange={(o) => !o && location.replace("/")} title="Signed out of this browser, but the server could not be reached to end its key.">
        <p className="text-ink-soft">If this is a shared computer, sign in again and choose “Sign out everywhere”.</p>
        <DialogActions>
          <Button variant="primary" onClick={() => location.replace("/")}>
            Done
          </Button>
        </DialogActions>
      </Dialog>
    </>
  );
}

/** StatusStrip is the event's stage (and a judge's scoring state) and the next date that matters, on every event screen. */
function StatusStrip({ d }: { d: EventDetail }) {
  const ev = d.event;
  const next = ev.stage !== "archived" ? nextDeadline(ev) : null;
  return (
    <div className="mt-3 flex flex-wrap items-center gap-x-3 gap-y-1 text-sm text-ink-soft" aria-label="Event status">
      <Badge tone="accent">{stageLabel(ev.stage)}</Badge>
      {d.role === "judge" && ev.stage !== "archived" && (
        <Badge tone={ev.judging_locked_at ? "down" : ev.scoring_open ? "ok" : "neutral"}>
          {ev.judging_locked_at ? "Scoring locked" : ev.scoring_open ? "Scoring open" : "Scoring not open"}
        </Badge>
      )}
      {next && (
        <span>
          {next[0]} · {dateTime(next[1], ev, next[2] ? { dateOnly: true } : {})}
        </span>
      )}
    </div>
  );
}
