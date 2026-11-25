// The owner dashboard (route /dashboard/*): the list of my sites is home;
// each site has its own page with its sections as tabs; the account has one.
//   /dashboard                       Your sites (and recently deleted)
//   /dashboard/new                   a new site from files
//   /dashboard/sites/{owner}/{name}  one site; the open tab is the #hash
//   /dashboard/account               the account; the open tab is the #hash
// Old links (/dashboard#site/{owner}/{name}, #account, #new, #bin) still land.
import { lazy, Suspense, useEffect, type ReactNode } from "react";
import { Navigate, Route, Routes, useLocation, useNavigate, useParams } from "react-router";
import { ApiError } from "@/lib/api";
import { AppShell } from "@/app-shell/AppShell";
import { Button } from "@/components/ui/button";
import { EmptyState } from "@/components/ui/empty-state";
import { Skeleton } from "@/components/ui/skeleton";
import { useMe } from "./data";
import { SitesHome } from "./SitesHome";

const SitePage = lazy(() => import("./site/SitePage").then((m) => ({ default: m.SitePage })));
const NewSite = lazy(() => import("./NewSite").then((m) => ({ default: m.NewSite })));
const Account = lazy(() => import("./account/Account").then((m) => ({ default: m.Account })));

export function Dashboard() {
  useOldLinks();
  return (
    <Routes>
      <Route index element={<Signed title="Your sites"><SitesHome /></Signed>} />
      <Route path="new" element={<Signed title="New site"><NewSite /></Signed>} />
      <Route path="account" element={<Signed title="Account"><Account /></Signed>} />
      <Route path="sites/:owner/:name" element={<SiteRoute />} />
      <Route path="*" element={<Navigate to="/dashboard" replace />} />
    </Routes>
  );
}

function SiteRoute() {
  const { name = "Site" } = useParams();
  return (
    <Signed title={name}>
      <SitePage />
    </Signed>
  );
}

/** useOldLinks moves the old dashboard's #hash addresses to their pages. */
function useOldLinks() {
  const { hash, pathname } = useLocation();
  const navigate = useNavigate();
  useEffect(() => {
    if (pathname !== "/dashboard" || !hash) return;
    const [what, owner, name] = hash.slice(1).split("/").map(decodeURIComponent);
    const to =
      what === "site" && owner && name
        ? "/dashboard/sites/" + encodeURIComponent(owner) + "/" + encodeURIComponent(name)
        : what === "account"
          ? "/dashboard/account"
          : what === "new"
            ? "/dashboard/new"
            : what === "bin"
              ? "/dashboard#deleted"
              : "";
    if (to && to !== pathname + hash) navigate(to, { replace: true });
  }, [hash, pathname, navigate]);
}

/**
 * Signed is the frame of every dashboard page: the app shell, and the page
 * once we know who is signed in. Signed out goes to sign-in and back here.
 */
function Signed({ title, children }: { title: string; children: ReactNode }) {
  const me = useMe();
  const { pathname, hash } = useLocation();
  const out = (me.error as ApiError | null)?.status === 401;
  useEffect(() => {
    if (out) window.location.replace("/signin?next=" + encodeURIComponent(pathname + hash));
  }, [out, pathname, hash]);
  return (
    <AppShell title={title}>
      {me.isPending || out ? (
        <PageLoading />
      ) : me.isError ? (
        <EmptyState title="Could not load your sites" action={<Button onClick={() => me.refetch()}>Try again</Button>}>
          {(me.error as Error).message}
        </EmptyState>
      ) : (
        <Suspense fallback={<PageLoading />}>{children}</Suspense>
      )}
    </AppShell>
  );
}

/** PageLoading holds the shape of a page title and a list while they load. */
export function PageLoading({ label = "Loading" }: { label?: string }) {
  return (
    <div role="status" aria-label={label} className="mt-8">
      <Skeleton className="h-8 w-48" />
      <Skeleton className="mt-6 h-12" />
      <div className="mt-6 grid gap-3">
        {[0, 1, 2, 3].map((i) => (
          <Skeleton key={i} className="h-16" />
        ))}
      </div>
    </div>
  );
}
