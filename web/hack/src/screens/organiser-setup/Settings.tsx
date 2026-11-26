// Settings (/e/{slug}/manage?s=settings&tab=…): the event's rules and
// administration, one section at a time in eleven tabs (navigation.json's
// settings_tabs). Public page content is on Event page. Each tab loads what
// it needs; an old #anchor link opens the tab that holds that section.
import type { ComponentType } from "react";
import { PageTitle } from "@kit/components/ui/page-shell";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@kit/components/ui/tabs";
import { useEventDetail } from "@/app/data";
import { Loading } from "@/app/states";
import type { EventDetail } from "@/lib/client";
import type { ScreenProps } from "@/routes/registry";
import { useTab } from "./common";
import { HASH_TAB, SETTINGS_TABS, TAB_LABEL, type SettingsTab } from "./settings";
import { DetailsTab, LinksTab, OrganisersTab } from "./settings-admin";
import { DangerTab } from "./settings-danger";
import { EntriesTab, RemindersTab } from "./settings-entries";
import { JudgingTab } from "./settings-judging";
import { NotificationsTab } from "./settings-notifications";
import { ResultsTab } from "./settings-results";
import { SignupTab, TeamsTab } from "./settings-signup";

export type TabProps = { d: EventDetail };

const BODY: Record<SettingsTab, ComponentType<TabProps>> = {
  signup: SignupTab,
  teams: TeamsTab,
  entries: EntriesTab,
  reminders: RemindersTab,
  judging: JudgingTab,
  results: ResultsTab,
  details: DetailsTab,
  organisers: OrganisersTab,
  links: LinksTab,
  notifications: NotificationsTab,
  danger: DangerTab,
};

export default function Settings({ match }: ScreenProps) {
  const d = useEventDetail(match.slug).data;
  const [tab, setTab] = useTab(SETTINGS_TABS, HASH_TAB);
  if (!d) return <Loading />;
  return (
    <>
      <PageTitle>Settings</PageTitle>
      <Tabs value={tab} onValueChange={(t) => setTab(t as SettingsTab)} className="mt-4">
        <TabsList aria-label="Settings sections">
          {SETTINGS_TABS.map((t) => (
            <TabsTrigger key={t} value={t}>
              {TAB_LABEL[t]}
            </TabsTrigger>
          ))}
        </TabsList>
        {SETTINGS_TABS.map((t) => {
          const Body = BODY[t];
          return (
            <TabsContent key={t} value={t}>
              <Body d={d} />
            </TabsContent>
          );
        })}
      </Tabs>
    </>
  );
}
