// The signed-in frame of every owner-app screen: a side rail on a wide
// screen, a top bar and a bottom bar on a phone. It knows who is signed in
// (GET /v1/me) to show Admin only to the operator, and signs out.
import { useEffect, type ReactNode } from "react";
import { Link, useLocation } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { useApi } from "@/lib/api";
import { cn } from "@kit/lib/utils";
import { Wordmark } from "@kit/components/ui/page-shell";
import { useToast } from "@kit/components/ui/toast";
import { AccountIcon, AdminIcon, DocsIcon, SignOutIcon, SitesIcon } from "./icons";

export type Me = { id: string; email: string; handle: string; operator: boolean };

export function useMe() {
  const call = useApi();
  return useQuery({ queryKey: ["me"], queryFn: () => call<Me>("GET", "/v1/me"), retry: false, staleTime: 60_000 });
}

type Item = { href: string; label: string; icon: ReactNode; on: (path: string) => boolean };

export function AppShell({ title, children }: { title: string; children: ReactNode }) {
  const me = useMe().data;
  const call = useApi();
  const toast = useToast();
  const { pathname } = useLocation();
  const items: Item[] = [
    { href: "/dashboard", label: "Sites", icon: <SitesIcon />, on: (p) => p.startsWith("/dashboard") && !p.startsWith("/dashboard/account") },
    { href: "/dashboard/account", label: "Account", icon: <AccountIcon />, on: (p) => p.startsWith("/dashboard/account") },
    ...(me?.operator ? [{ href: "/admin", label: "Admin", icon: <AdminIcon />, on: (p: string) => p.startsWith("/admin") }] : []),
    { href: "/docs", label: "Docs", icon: <DocsIcon />, on: () => false },
  ];
  const signOut = async () => {
    try {
      await call("POST", "/v1/auth/signout", {});
      window.location.assign("/");
    } catch (e) {
      toast((e as Error).message, { bad: true });
    }
  };
  // Tells what floats over the page (the toast) how much room the bars take.
  useEffect(() => {
    document.documentElement.dataset.shell = "app";
    return () => void delete document.documentElement.dataset.shell;
  }, []);

  return (
    <div className="min-h-dvh md:grid md:grid-cols-[224px_minmax(0,1fr)]">
      <title>{title + " · Simple Host"}</title>
      {/* Wide: the side rail. */}
      <aside className="sticky top-0 hidden h-dvh flex-col border-r-[1.5px] border-dashed border-ink-soft px-3 py-4 md:flex">
        <a href="/" className="mb-6 inline-flex min-h-11 items-center px-3 no-underline" aria-label="simple-host home">
          <Wordmark />
        </a>
        <nav aria-label="Main" className="flex flex-col gap-1">
          {items.map((it) => (
            <Link
              key={it.href}
              to={it.href}
              reloadDocument={it.href === "/docs"}
              aria-current={it.on(pathname) ? "page" : undefined}
              className={cn(
                "flex min-h-11 items-center gap-3 rounded-tab px-3 text-base font-medium text-ink-soft no-underline hover:bg-paper-lo hover:text-ink",
                it.on(pathname) && "bg-accent-soft text-ink",
              )}
            >
              {it.icon}
              {it.label}
            </Link>
          ))}
        </nav>
        <div className="mt-auto border-t border-rule pt-3">
          {me && <p className="mb-1 px-3 text-sm text-ink-soft [overflow-wrap:anywhere]">{me.email}</p>}
          <button
            type="button"
            onClick={signOut}
            className="flex min-h-11 w-full cursor-pointer items-center gap-3 rounded-tab px-3 text-base font-medium text-ink-soft hover:bg-paper-lo hover:text-ink"
          >
            <SignOutIcon />
            Sign out
          </button>
        </div>
      </aside>

      {/* Phone: a top bar with the wordmark. */}
      <header className="flex items-center justify-between border-b-[1.5px] border-dashed border-ink-soft px-4 py-1.5 md:hidden">
        <a href="/" className="inline-flex min-h-11 items-center no-underline" aria-label="simple-host home">
          <Wordmark className="text-2xl" />
        </a>
        {me && (
          <button type="button" onClick={signOut} className="inline-flex min-h-11 cursor-pointer items-center gap-2 px-1 text-sm text-ink-soft hover:text-ink">
            <SignOutIcon />
            Sign out
          </button>
        )}
      </header>

      <div className="flex min-w-0 flex-col">
        <main className="mx-auto w-full max-w-page flex-1 px-4 pb-8 md:px-8">{children}</main>
        <footer className="mx-auto w-full max-w-page px-4 pb-24 md:px-8 md:pb-6">
          <nav aria-label="About" className="flex flex-wrap gap-x-5 border-t border-rule pt-2">
            {[
              ["/privacy", "Privacy"],
              ["/terms", "Terms"],
              ["/support", "Support"],
            ].map(([href, label]) => (
              <a key={href} href={href} className="inline-flex min-h-11 min-w-11 items-center justify-center text-sm text-ink-soft no-underline hover:text-ink">
                {label}
              </a>
            ))}
          </nav>
        </footer>
      </div>

      {/* Phone: the bottom bar (signing out is in the top bar). */}
      <nav
        aria-label="Main"
        className="fixed inset-x-0 bottom-0 z-30 grid auto-cols-fr grid-flow-col border-t-[1.5px] border-ink-soft bg-card pb-[env(safe-area-inset-bottom)] md:hidden"
      >
        {items.map((it) => (
          <Link
            key={it.href}
            to={it.href}
            reloadDocument={it.href === "/docs"}
            aria-current={it.on(pathname) ? "page" : undefined}
            className={cn(
              "flex min-h-14 flex-col items-center justify-center gap-0.5 text-xs text-ink-soft no-underline",
              it.on(pathname) && "text-accent",
            )}
          >
            {it.icon}
            {it.label}
          </Link>
        ))}
      </nav>
    </div>
  );
}
