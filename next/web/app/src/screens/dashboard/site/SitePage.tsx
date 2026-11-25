// One site: its name, address and state at the top with Open site (the one
// filled button), then its sections as tabs. The open tab is the #hash.
import { Link, useLocation, useNavigate, useParams } from "react-router";
import { ApiError } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { CopyButton } from "@/components/ui/copy";
import { EmptyState } from "@/components/ui/empty-state";
import { Notice } from "@/components/ui/notice";
import { Skeleton } from "@/components/ui/skeleton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Badge } from "@/components/ui/badge";
import { ACCESS_WORDS, host, useChange, useMe, useSite, type Site } from "../data";
import { External, SiteState } from "../bits";
import { Visitors } from "./Visitors";
import { WhoCanOpen } from "./WhoCanOpen";
import { Files } from "./Files";
import { SavedData } from "./SavedData";
import { Address } from "./Address";
import { Alerts } from "./Alerts";
import { Settings } from "./Settings";

const TABS = [
  ["visitors", "Visitors"],
  ["access", "Who can open"],
  ["files", "Files and versions"],
  ["data", "Saved data"],
  ["address", "Address"],
  ["alerts", "Alerts"],
  ["settings", "Settings"],
] as const;

export function SitePage() {
  const { owner = "", name = "" } = useParams();
  const q = useSite(owner, name);
  return (
    <div className="pt-4">
      <Link to="/dashboard" className="inline-flex min-h-11 items-center text-sm">
        ← Your sites
      </Link>
      {q.isPending ? (
        <Loading />
      ) : q.isError ? (
        (q.error as ApiError).status === 404 ? (
          <EmptyState
            title={"No site called " + name}
            action={
              <Button asChild>
                <Link to="/dashboard">Back to your sites</Link>
              </Button>
            }
          >
            It may have been deleted, renamed with its owner's new name, or belong to a team you are not in.
          </EmptyState>
        ) : (
          <EmptyState title={"Could not load " + name} action={<Button onClick={() => q.refetch()}>Try again</Button>}>
            {(q.error as Error).message}
          </EmptyState>
        )
      ) : (
        <Loaded site={q.data} />
      )}
    </div>
  );
}

function Loading() {
  return (
    <div role="status" aria-label="Loading the site">
      <Skeleton className="mt-3 h-9 w-56" />
      <Skeleton className="mt-2 h-5 w-72 max-w-full" />
      <Skeleton className="mt-6 h-11" />
      <Skeleton className="mt-6 h-40" />
    </div>
  );
}

function Loaded({ site }: { site: Site }) {
  const me = useMe().data!;
  const { hash } = useLocation();
  const navigate = useNavigate();
  const change = useChange(site);
  const tab = TABS.some(([t]) => "#" + t === hash) ? hash.slice(1) : "visitors";
  const show = (t: string) => navigate({ hash: t === "visitors" ? "" : t }, { replace: true });

  return (
    <>
      <header className="mt-2 flex flex-wrap items-start justify-between gap-x-6 gap-y-3">
        <div className="min-w-0 flex-[1_1_18rem]">
          <h1 className="type-page break-all">{site.name}</h1>
          <p className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1">
            <a href={site.url} target="_blank" rel="noopener" className="inline-flex min-h-11 min-w-0 items-center break-all">
              {host(site.url)}
            </a>
            <CopyButton text={site.url} label="Copy address" variant="ghost" className="min-h-11 px-3 text-sm" />
          </p>
          <p className="mt-1 flex flex-wrap items-center gap-2 text-sm text-ink-soft">
            <SiteState s={site} />
            <Badge>{ACCESS_WORDS[site.access] || site.access}</Badge>
            {site.owner !== me.handle && <span>Team {site.owner}</span>}
          </p>
        </div>
        {!site.taken_down && (
          <Button variant="primary" asChild className="gap-2 px-5">
            <a href={site.url} target="_blank" rel="noopener">
              Open site <External />
            </a>
          </Button>
        )}
      </header>

      {site.taken_down && (
        <Notice tone="bad" title="The operator took this site down">
          Every address shows “This site has been taken down” and changes are refused until it is restored. Nothing has been deleted. If you
          think this is a mistake, write to <a href="/support">support</a>.
        </Notice>
      )}
      {site.offline && !site.taken_down && (
        <Notice
          tone="bad"
          title="This site is offline"
          action={<Button onClick={() => change({ offline: false }, site.name + " is back online.", () => void change({ offline: true }, site.name + " is offline."))}>Put back online</Button>}
        >
          Visitors see “This site is offline” and cannot save anything. Its files, versions and saved data are kept.
        </Notice>
      )}

      <Tabs value={tab} onValueChange={show} className="mt-5">
        <TabsList aria-label={"Parts of " + site.name}>
          {TABS.map(([t, label]) => (
            <TabsTrigger key={t} value={t}>
              {label}
            </TabsTrigger>
          ))}
        </TabsList>
        <TabsContent value="visitors">
          <Visitors site={site} />
        </TabsContent>
        <TabsContent value="access">
          <WhoCanOpen site={site} />
        </TabsContent>
        <TabsContent value="files">
          <Files site={site} />
        </TabsContent>
        <TabsContent value="data">
          <SavedData site={site} />
        </TabsContent>
        <TabsContent value="address">
          <Address site={site} />
        </TabsContent>
        <TabsContent value="alerts">
          <Alerts site={site} />
        </TabsContent>
        <TabsContent value="settings">
          <Settings site={site} />
        </TabsContent>
      </Tabs>
    </>
  );
}
