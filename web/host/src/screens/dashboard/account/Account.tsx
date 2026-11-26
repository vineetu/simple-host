// Account: profile (name, public page, one domain for all sites, what the
// account may hold), keys and connected apps, notifications on this device,
// teams, and the account's data (download, delete). The open tab is the #hash.
import { useLocation, useNavigate } from "react-router";
import { PageTitle } from "@kit/components/ui/page-shell";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@kit/components/ui/tabs";
import { useMe } from "../data";
import { Profile } from "./Profile";
import { KeysAndApps } from "./KeysAndApps";
import { Notifications } from "./Notifications";
import { Teams } from "./Teams";
import { YourData } from "./YourData";

const TABS = [
  ["profile", "Profile"],
  ["keys", "Keys and apps"],
  ["notifications", "Notifications"],
  ["teams", "Teams"],
  ["data", "Your data"],
] as const;

export function Account() {
  const me = useMe().data!;
  const { hash } = useLocation();
  const navigate = useNavigate();
  const tab = TABS.some(([t]) => "#" + t === hash) ? hash.slice(1) : "profile";
  return (
    <>
      <PageTitle sub={me.email}>Account</PageTitle>
      <Tabs value={tab} onValueChange={(t) => navigate({ hash: t === "profile" ? "" : t }, { replace: true })} className="mt-4">
        <TabsList aria-label="Account sections">
          {TABS.map(([t, label]) => (
            <TabsTrigger key={t} value={t}>
              {label}
            </TabsTrigger>
          ))}
        </TabsList>
        <TabsContent value="profile">
          <Profile me={me} />
        </TabsContent>
        <TabsContent value="keys">
          <KeysAndApps me={me} />
        </TabsContent>
        <TabsContent value="notifications">
          <Notifications me={me} />
        </TabsContent>
        <TabsContent value="teams">
          <Teams me={me} />
        </TabsContent>
        <TabsContent value="data">
          <YourData me={me} />
        </TabsContent>
      </Tabs>
    </>
  );
}
