// Event page (/e/{slug}/manage?s=event-page&tab=…): everything that makes up
// the public event page, one tab at a time: Basics, About, Schedule, Tracks,
// Sponsors, FAQ, Look, Website. The tab is the address's ?tab= (Back steps
// through tabs; old #hash links land on their tab; the old Announcements
// part goes to its own screen). The content document and the website state
// are read once on entry and kept across tab switches. Switching away from
// a form with unsaved changes asks first (the kit's unsaved guard: tabs are addresses).
import { useEffect, type ReactNode } from "react";
import { useLocation, useNavigate } from "react-router";
import { Button } from "@kit/components/ui/button";
import { PageTitle } from "@kit/components/ui/page-shell";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@kit/components/ui/tabs";
import { useEventDetail } from "@/app/data";
import { inApp } from "@/app/nav";
import { Failed, Loading } from "@/app/states";
import type { ScreenProps } from "@/routes/registry";
import { href } from "@/routes/resolve";
import { useTab, type Ev } from "./common";
import { HASH_TAB, TAB_LABEL, TABS, wantsAnnouncements, type Tab } from "./eventpage-data";
import { About, Basics } from "./eventpage-forms";
import { FaqTab, Schedule, Sponsors } from "./eventpage-lists";
import { Look } from "./eventpage-look";
import { Tracks } from "./eventpage-tracks";
import { useContent, useWebsite, type TabProps } from "./eventpage-ui";
import { Website } from "./eventpage-website";
import { LockNote } from "@/shared/locks";

const BODY: Record<Tab, (p: TabProps) => ReactNode> = { basics: Basics, about: About, schedule: Schedule, tracks: Tracks, sponsors: Sponsors, faq: FaqTab, look: Look, website: Website };

export default function EventPage({ match }: ScreenProps) {
  const slug = match.slug!;
  const { search, hash } = useLocation();
  const navigate = useNavigate();
  const detail = useEventDetail(slug);
  const content = useContent(slug);
  const website = useWebsite(slug);
  const [tab, setTab] = useTab(TABS, HASH_TAB);
  const toAnnouncements = wantsAnnouncements(search, hash);
  useEffect(() => {
    if (!toAnnouncements) return;
    const to = href("organiser", slug, "announcements");
    if (inApp(to)) navigate(to, { replace: true });
    else location.replace(to);
  }, [toAnnouncements, slug, navigate]);

  if (toAnnouncements || detail.isPending) return <Loading />;
  if (detail.isError) return <Failed error={detail.error} retry={() => detail.refetch()} />;
  const d = detail.data;
  const ev = d.event as Ev;
  const url = String(ev.url || "");
  const p: TabProps = { slug, d, ev, admin: !!d.admin_view, ended: ev.stage === "archived", content, website };
  const counts: Partial<Record<Tab, number>> = content.data
    ? { schedule: (content.data.schedule || []).length + (content.data.fixed_schedule || []).length, sponsors: (content.data.sponsors || []).length, faq: (content.data.faq || []).length }
    : {};
  const go = (t: string) => {
    if (t === tab) return;
    setTab(t as Tab);
  };
  const Body = BODY[tab];

  return (
    <>
      <div className="flex flex-wrap items-end justify-between gap-x-4 gap-y-2">
        <PageTitle
          sub={
            <>
              What visitors see at{" "}
              <a href={url} target="_blank" rel="noopener noreferrer">
                {url.replace(/^https?:\/\//, "")}
              </a>{" "}
              before they sign up.
            </>
          }
        >
          Event page
        </PageTitle>
        <Button asChild>
          <a href={url} target="_blank" rel="noopener noreferrer">
            View public page <span aria-hidden="true">↗</span>
            <span className="sr-only"> (opens in a new tab)</span>
          </a>
        </Button>
      </div>
      {p.admin && <LockNote reason="Viewing as platform admin. Everything here is read only." />}
      <Tabs value={tab} onValueChange={go} className="mt-4">
        <TabsList aria-label="Event page sections">
          {TABS.map((t) => (
            <TabsTrigger key={t} value={t}>
              {TAB_LABEL[t]}
              {counts[t] ? <span className="ml-1.5 rounded-tag bg-paper-lo px-1.5 text-sm text-ink-soft">{counts[t]}</span> : null}
            </TabsTrigger>
          ))}
        </TabsList>
        <TabsContent value={tab}>
          {/* A fresh form per tab: leaving a tab drops what it had not saved. */}
          <Body key={tab} {...p} />
        </TabsContent>
      </Tabs>
    </>
  );
}
