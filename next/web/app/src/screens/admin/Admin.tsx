// The operator's page (route /admin): Overview, Users, Sites, API, Moderation,
// and Tools on a self-hosted install. The open tab is the address's #hash.
import { useEffect, useState } from "react";
import { useLocation, useNavigate } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { ApiError, useApi } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { EmptyState } from "@/components/ui/empty-state";
import { PageTitle } from "@/components/ui/page-shell";
import { Skeleton } from "@/components/ui/skeleton";
import { AppShell } from "@/app-shell/AppShell";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useAdmin } from "./data";
import { ActionsProvider } from "./actions";
import { OverviewTab } from "./Overview";
import { UsersTab, usersStart } from "./Users";
import { SitesTab, sitesStart } from "./Sites";
import { GrowthTab } from "./Growth";
import { ModerationTab } from "./Moderation";
import { ToolsTab } from "./Tools";

const TABS = [
  ["overview", "Overview"],
  ["users", "Users"],
  ["sites", "Sites"],
  ["api", "API"],
  ["moderation", "Moderation"],
  ["tools", "Tools"],
] as const;

export function Admin() {
  const q = useAdmin();
  return (
    <AppShell title="Admin">
      <PageTitle>Admin</PageTitle>
      {q.isPending ? (
        <Loading />
      ) : q.isError ? (
        <Refused error={q.error as ApiError} retry={() => q.refetch()} />
      ) : (
        <ActionsProvider>
          <Loaded />
        </ActionsProvider>
      )}
    </AppShell>
  );
}

/** Loading is the page's shape while the lists come, so nothing moves when they arrive. */
function Loading() {
  return (
    <div role="status" aria-label="Loading the admin page" className="mt-4">
      <Skeleton className="h-12" />
      <Skeleton className="mt-6 h-24" />
      <div className="mt-8 grid gap-3">
        {[0, 1, 2, 3, 4].map((i) => (
          <Skeleton key={i} className="h-10" />
        ))}
      </div>
    </div>
  );
}

function Loaded() {
  const d = useAdmin().data!;
  const loc = useLocation();
  const navigate = useNavigate();
  const open = TABS.filter(([t]) => t !== "tools" || d.overview.self_hosted).map(([t]) => t as string);
  const hash = loc.hash.replace(/^#/, "");
  const tab = open.includes(hash) ? hash : "overview";
  const show = (t: string) => navigate({ hash: t === "overview" ? "" : t }, { replace: true });
  const [users, setUsers] = useState(usersStart);
  const [sites, setSites] = useState(sitesStart);

  return (
    <Tabs value={tab} onValueChange={show} className="mt-4">
      <TabsList aria-label="Admin sections">
        {TABS.filter(([t]) => open.includes(t)).map(([t, label]) => (
          <TabsTrigger key={t} value={t}>
            {label}
          </TabsTrigger>
        ))}
      </TabsList>
      <TabsContent value="overview">
        <OverviewTab
          d={d}
          go={(to) => {
            if (to === "sites-by-views") {
              setSites({ ...sites, sorting: [{ id: "views", desc: true }] });
              show("sites");
            } else show("users");
          }}
        />
      </TabsContent>
      <TabsContent value="users">
        <UsersTab
          d={d}
          st={users}
          set={setUsers}
          openSites={(handle) => {
            setSites({ ...sites, q: handle, filter: "all" });
            show("sites");
          }}
        />
      </TabsContent>
      <TabsContent value="sites">
        <SitesTab d={d} st={sites} set={setSites} />
      </TabsContent>
      <TabsContent value="api">
        <GrowthTab />
      </TabsContent>
      <TabsContent value="moderation">
        <ModerationTab d={d} />
      </TabsContent>
      {d.overview.self_hosted && (
        <TabsContent value="tools">
          <ToolsTab />
        </TabsContent>
      )}
    </Tabs>
  );
}

/** Refused: not signed in goes to sign-in; signed in as someone else is told so. */
function Refused({ error, retry }: { error: ApiError; retry: () => void }) {
  const call = useApi();
  const me = useQuery({
    queryKey: ["me"],
    queryFn: () => call("GET", "/v1/me"),
    enabled: error.status === 403,
    retry: false,
  });
  const signIn = error.status === 401 || (me.error as ApiError | null)?.status === 401;
  useEffect(() => {
    if (signIn) window.location.replace("/signin?next=/admin");
  }, [signIn]);
  if (signIn || (error.status === 403 && me.isPending)) return null;
  if (error.status === 403)
    return (
      <EmptyState
        title="Only the operator can open this page"
        action={
          <Button asChild>
            <a href="/dashboard">Back to your sites</a>
          </Button>
        }
      >
        You are signed in with an account that is not the operator's.
      </EmptyState>
    );
  return (
    <EmptyState title="Could not load the admin page" action={<Button onClick={retry}>Try again</Button>}>
      {error.message}
    </EmptyState>
  );
}
