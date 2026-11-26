// The one route: which screen the address is, and the frame it sits in.
// Event screens get the event loaded and checked first, as the old app.js
// route() does: signed out goes to sign-in; an event the person is not in
// says so; someone on the wrong area's address is moved to their own; a
// participant still waiting for approval (or turned down) sees only that.
// A screen this app has not rebuilt (or that is not live yet) is the old
// app's: the page is loaded again and the server serves it.
import { lazy, Suspense, useEffect, useState, type ReactNode } from "react";
import { useLocation, useNavigate } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { Button } from "@kit/components/ui/button";
import { EmptyState } from "@kit/components/ui/empty-state";
import { Section } from "@kit/components/ui/page-shell";
import type { ApiError } from "@kit/api/call";
import { useHack, type EventDetail, type Role } from "@/lib/client";
import { apiKey, goSignIn, joinPath } from "@/lib/session";
import { arrived, hop, liveScreens } from "@/routes/live";
import { registry } from "@/routes/registry";
import { eventHome, href, resolveScreen, type Match } from "@/routes/resolve";
import { useEventDetail } from "./data";
import { HackShell } from "./HackShell";
import { useLeave } from "./Leave";
import { Wordmark } from "./marks";
import { inApp } from "./nav";
import { Failed, Loading } from "./states";

// The organiser's Ask panel sits beside every organiser screen, mounted once here (so it stays open across screens).
const Assistant = lazy(() => import("@/screens/organiser-run/Assistant").then((m) => ({ default: m.Assistant })));

const ABILITY: Record<Role, string> = { organiser: "manage", judge: "judge", participant: "take_part" };

function useReplace() {
  const navigate = useNavigate();
  return (url: string) => (inApp(url) ? navigate(url, { replace: true }) : location.replace(url));
}

function Body({ id, match }: { id: string; match: Match }) {
  const def = registry[id];
  return (
    <Suspense fallback={<Loading />}>
      <def.Component match={match} />
    </Suspense>
  );
}

/** Locked is the participant who is not approved yet, or was turned down (app.js A20). */
function Locked({ d }: { d: EventDetail }) {
  const [openLeave, dialog] = useLeave();
  const pending = d.me?.approval_status === "pending";
  const ended = d.event.stage === "archived";
  const copy = ended
    ? pending
      ? "This event has ended, so you can’t withdraw. It stays on your Home as a record."
      : "This event has ended, so nobody can leave it. It stays on your Home as a record."
    : pending
      ? "The organisers stop seeing it, along with your answers. You can apply again while sign-up is open."
      : "Leaving removes your answers. You can then apply again while the event takes applications, and the organisers decide again.";
  return (
    <>
      <EmptyState title={pending ? "Your application is waiting for the organisers" : "Your application wasn't accepted this time"}>
        {pending ? "You'll see their answer here. Ask them if you have questions." : "Ask an organiser if you have questions."}
      </EmptyState>
      <Section title={pending ? "Withdraw your application" : "Leave this event"} className="rounded-field border border-bad px-4 py-3">
        <p className="text-sm text-ink-soft">{copy}</p>
        <Button variant="danger" className="mt-3" disabled={ended} onClick={() => openLeave(d.event.slug, d.event.title)}>
          {pending ? "Withdraw application" : "Leave event"}
        </Button>
      </Section>
      {dialog}
    </>
  );
}

function NotInEvent({ slug, match }: { slug: string; match: Match }) {
  const api = useHack();
  const pub = useQuery({ queryKey: ["event-public", slug], queryFn: () => api.event.publicView(slug).catch(() => null) });
  if (pub.isPending) return <HackShell title="Simple Hack" where={{ screen: match.id }}>{<Loading />}</HackShell>;
  const title = pub.data ? `You're not in ${pub.data.title}` : "Event not found";
  return (
    <HackShell title={title} where={{ screen: "event-not-found" }}>
      <EmptyState
        title={title}
        action={
          <div className="flex flex-wrap justify-center gap-2.5">
            <Button variant="primary" asChild>
              <a href="/events">Home</a>
            </Button>
            <Button asChild>
              <a href="/directory">Find a hackathon</a>
            </Button>
          </div>
        }
      >
        {pub.data ? "You're signed in, but not a member of this event: you may have left it, or never joined." : "It may have been deleted, or the link is wrong."}
      </EmptyState>
    </HackShell>
  );
}

function EventScreen({ match }: { match: Match }) {
  const { search } = useLocation();
  const q = useEventDetail(match.slug);
  const replace = useReplace();
  const d = q.data;
  const wrongArea = !!d && (d.admin_view ? match.role !== "organiser" : !d.can?.[ABILITY[match.role!]]);
  useEffect(() => {
    if (!d || !wrongArea) return;
    replace(d.admin_view ? href("organiser", d.event.slug, "overview") : eventHome(d.role, d.event.slug));
  });
  const def = registry[match.id];
  if (q.isError) {
    const err = q.error as ApiError;
    if (err.status === 404 || err.code === "event_not_found") return <NotInEvent slug={match.slug!} match={match} />;
    if (err.status === 401) return null;
    return (
      <HackShell title="Simple Hack" where={{ screen: match.id }}>
        <Failed error={err} retry={() => q.refetch()} />
      </HackShell>
    );
  }
  if (!d || wrongArea)
    return (
      <HackShell title={def.title} where={{ screen: match.id }}>
        <Loading />
      </HackShell>
    );
  if (!["organiser", "participant", "judge"].includes(d.role))
    return (
      <HackShell title="Simple Hack" where={{ screen: match.id }}>
        <Failed error={new Error("You do not have access to this event")} retry={() => q.refetch()} />
      </HackShell>
    );
  const locked = match.role === "participant" && d.me?.approval_status && d.me.approval_status !== "approved";
  // The frame shows the area of the address (an organiser who also judges sees the judge area on /judge).
  const view: EventDetail = { ...d, role: match.role! };
  return (
    <HackShell title={def.title} where={{ screen: match.id, event: view, section: match.section, params: new URLSearchParams(search) }}>
      {locked ? <Locked d={d} /> : <Body id={match.id} match={match} />}
      {match.role === "organiser" && (
        <Suspense fallback={null}>
          <Assistant slug={d.event.slug} screen={match.id} />
        </Suspense>
      )}
    </HackShell>
  );
}

function PublicFrame({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="flex min-h-dvh flex-col">
      <title>{title + " · Simple Hack"}</title>
      <header className="flex items-center gap-2 border-b border-rule px-4 py-1.5 md:px-6">
        <a href="/" className="mr-auto inline-flex min-h-11 items-center no-underline">
          <Wordmark />
        </a>
        {apiKey() && (
          <a href="/events" className="inline-flex min-h-11 items-center px-2 text-ink-soft no-underline hover:text-ink">
            Home
          </a>
        )}
      </header>
      <main className="mx-auto w-full max-w-page flex-1 px-4 pb-8 md:px-6">{children}</main>
    </div>
  );
}

/** Screen renders whatever screen the address is. */
export function Screen() {
  const { pathname, search } = useLocation();
  const match = resolveScreen(pathname, search);
  const def = registry[match.id];
  const live = liveScreens();
  const here = !!def && (!live || live.has(match.id));
  const [stuck, setStuck] = useState(false);
  const needKey = !apiKey() && !joinPath(pathname) && def?.kind !== "public";
  useEffect(() => {
    arrived();
    if (!here) setStuck(!hop(location.href));
    else if (needKey) goSignIn();
  }, [here, needKey, pathname, search]);
  if (!here)
    return stuck ? (
      <PublicFrame title="Simple Hack">
        <EmptyState title="This screen could not load" action={<Button onClick={() => location.reload()}>Reload</Button>}>
          Reload the page, or go to <a href="/events">Home</a>.
        </EmptyState>
      </PublicFrame>
    ) : null;
  if (needKey) return null;
  if (def.kind === "public")
    return (
      <PublicFrame title={def.title}>
        <Body id={match.id} match={match} />
      </PublicFrame>
    );
  if (def.kind === "event") return <EventScreen key={match.slug} match={match} />;
  return (
    <HackShell title={def.title} where={{ screen: match.id, params: new URLSearchParams(search) }}>
      <Body id={match.id} match={match} />
    </HackShell>
  );
}
