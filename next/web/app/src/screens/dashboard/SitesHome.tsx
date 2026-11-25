// Your sites: the dashboard's home. The list (search, filter, sort; a table
// from 700 px, cards on a phone), invitations to teams, the first steps for a
// new account, and the sites deleted in the last 7 days.
import { useState } from "react";
import { Link } from "react-router";
import { useQueryClient } from "@tanstack/react-query";
import type { ColumnDef, SortingState } from "@tanstack/react-table";
import { useApi } from "@/lib/api";
import { searchMatches, dateOf } from "@/lib/format";
import { Chips } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardLead, CardTitle } from "@/components/ui/card";
import { CopyButton } from "@/components/ui/copy";
import { DataTable } from "@/components/ui/data-table";
import { EmptyState } from "@/components/ui/empty-state";
import { Field, Input } from "@/components/ui/input";
import { Notice } from "@/components/ui/notice";
import { PageTitle } from "@/components/ui/page-shell";
import { Skeleton } from "@/components/ui/skeleton";
import { useToast } from "@/components/ui/toast";
import { ACCESS_WORDS, host, pagePath, publicPage, sitePath, useMe, useSites, validName, type Me, type SiteItem } from "./data";
import { Ago, External, Part, Row, SiteState } from "./bits";

type Filter = "all" | "live" | "offline" | "private" | "team";

export function SitesHome() {
  const me = useMe().data!;
  if (!me.handle) return <FirstHandle me={me} />;
  return <Home me={me} />;
}

function Home({ me }: { me: Me }) {
  const sites = useSites();
  const all = sites.data || [];
  const pub = all.find((s) => s.owner === me.handle);
  const page = pub ? publicPage(pub.url, pub.name, me.handle) : "";
  return (
    <>
      <div className="flex items-end justify-between gap-4">
        <PageTitle
          sub={
            <>
              {me.display_name || "@" + me.handle}
              {page && (
                <>
                  {" · "}
                  <a href={page} target="_blank" rel="noopener">
                    your public page
                  </a>
                </>
              )}
            </>
          }
        >
          Your sites
        </PageTitle>
        <Button variant="primary" asChild className="flex-none px-5">
          <Link to="/dashboard/new">New site</Link>
        </Button>
      </div>
      <Invitations me={me} />
      {sites.isPending ? (
        <ListLoading />
      ) : sites.isError ? (
        <EmptyState title="Could not load your sites" action={<Button onClick={() => sites.refetch()}>Try again</Button>}>
          {(sites.error as Error).message}
        </EmptyState>
      ) : all.length === 0 ? (
        <NoSites me={me} />
      ) : (
        <>
          {!me.apps.length && <ConnectApp />}
          <SiteList me={me} all={all} />
        </>
      )}
      <Deleted me={me} />
    </>
  );
}

function ListLoading() {
  return (
    <div role="status" aria-label="Loading your sites" className="mt-6">
      <Skeleton className="h-12" />
      <Skeleton className="mt-3 h-11 w-80 max-w-full" />
      <div className="mt-4 grid gap-2">
        {[0, 1, 2, 3].map((i) => (
          <Skeleton key={i} className="h-14" />
        ))}
      </div>
    </div>
  );
}

const mcp = () => window.location.origin + "/mcp";

/** ConnectApp is the one step that makes the rest work: the address to paste into an AI app. */
function ConnectApp({ big }: { big?: boolean }) {
  return (
    <section aria-label="Connect your AI app" className={big ? "" : "my-5 rounded-field border-[1.5px] border-dashed border-ink-soft px-4 py-3"}>
      <p className={big ? "type-section" : "font-semibold"}>Connect your AI app</p>
      <p className="mt-0.5 text-sm text-ink-soft">
        Paste this address into Claude, ChatGPT or any AI app that takes a custom connector. Then ask it to make or change a site.
      </p>
      <div className="mt-2.5 flex flex-wrap items-center gap-2">
        <code className="min-w-0 flex-[1_1_14rem] rounded-field border border-rule bg-card px-3 py-2.5 font-mono text-sm break-all">{mcp()}</code>
        <CopyButton text={mcp()} label="Copy address" />
        <Button asChild variant="ghost" className="px-3">
          <a href="/install">Steps for each app</a>
        </Button>
      </div>
    </section>
  );
}

function NoSites({ me }: { me: Me }) {
  return (
    <div className="mx-auto my-8 max-w-prose rounded-wobble border-[1.5px] border-dashed border-ink-soft px-5 py-6 md:px-8">
      <p className="type-section text-xl">No sites yet</p>
      <p className="mt-1 text-ink-soft">
        The quickest way is to ask your AI app: “Make me a one-page site for my bakery on Simple Host.” Your sites appear here, at addresses
        under <strong className="text-ink">{me.handle}</strong>.
      </p>
      {!me.apps.length && (
        <div className="mt-5">
          <ConnectApp big />
        </div>
      )}
      <p className="mt-5 text-sm text-ink-soft">
        Have the files already?{" "}
        <Link to="/dashboard/new">Publish them yourself</Link>.
      </p>
    </div>
  );
}

const FILTERS: (readonly [Filter, string])[] = [
  ["all", "All"],
  ["live", "Live"],
  ["offline", "Offline"],
  ["private", "Private"],
  ["team", "Team sites"],
];

const t = (s: string) => Date.parse(s) || 0;
const label = (f: Filter) => FILTERS.find(([v]) => v === f)![1].replace(" sites", "");

function SiteList({ me, all }: { me: Me; all: SiteItem[] }) {
  const [q, setQ] = useState("");
  const [filter, setFilter] = useState<Filter>("all");
  const [sorting, setSorting] = useState<SortingState>([{ id: "changed", desc: true }]);
  const team = all.some((s) => s.owner !== me.handle);
  const keep = (s: SiteItem) =>
    filter === "all" ||
    (filter === "live" && !s.offline && !s.taken_down) ||
    (filter === "offline" && (s.offline || !!s.taken_down)) ||
    (filter === "private" && s.access !== "open") ||
    (filter === "team" && s.owner !== me.handle);
  const rows = all.filter((s) => keep(s) && searchMatches(q, s.name + " " + s.owner + " " + host(s.url)));

  const columns: ColumnDef<SiteItem>[] = [
    {
      id: "name",
      header: "Site",
      accessorKey: "name",
      cell: ({ row: { original: s } }) => (
        <div className="min-w-0">
          <Link to={pagePath(s)} className="font-semibold no-underline hover:underline" aria-label={"Manage " + s.name}>
            {s.name}
          </Link>
          {s.owner !== me.handle && <span className="ml-2 text-sm font-normal text-ink-soft">team {s.owner}</span>}
          <div className="truncate text-sm font-normal text-ink-soft" title={host(s.url)}>{host(s.url)}</div>
          <div className="text-sm font-normal text-ink-soft md:hidden">
            Changed <Ago at={s.changed_at} />
            {s.access !== "open" && " · " + ACCESS_WORDS[s.access].toLowerCase()}
          </div>
        </div>
      ),
      meta: { phone: "title", wrap: true, sortNames: ["A to Z", "Z to A"] },
    },
    { id: "access", header: "Who can open", enableSorting: false, cell: ({ row }) => ACCESS_WORDS[row.original.access] || row.original.access, meta: { phone: "hide" } },
    {
      id: "changed",
      header: "Changed",
      accessorFn: (s) => t(s.changed_at),
      sortDescFirst: true,
      cell: ({ row }) => <Ago at={row.original.changed_at} />,
      meta: { phone: "hide", sortNames: ["Newest first", "Oldest first"] },
    },
    { id: "state", header: "Status", enableSorting: false, cell: ({ row }) => <SiteState s={row.original} />, meta: { phone: "end" } },
    {
      id: "open",
      header: () => <span className="sr-only">Open</span>,
      enableSorting: false,
      cell: ({ row: { original: s } }) => (
        <Button asChild variant="ghost" className="gap-1.5 px-3 text-sm whitespace-nowrap">
          <a href={s.url} target="_blank" rel="noopener" aria-label={"Open " + s.name + " in a new tab"}>
            Open <External />
          </a>
        </Button>
      ),
      meta: { phone: "end", align: "right" },
    },
  ];

  return (
    <section aria-label="Sites" className="mt-6">
      <Input
        type="search"
        value={q}
        onChange={(e) => setQ(e.target.value)}
        placeholder="Search your sites"
        aria-label="Search your sites"
        autoComplete="off"
        spellCheck={false}
      />
      <Chips label="Show" options={team ? FILTERS : FILTERS.filter(([f]) => f !== "team")} value={filter} onChange={setFilter} />
      <DataTable
        label="Your sites"
        data={rows}
        columns={columns}
        getRowId={(s) => s.owner + "/" + s.name}
        sorting={sorting}
        onSortingChange={setSorting}
        empty={
          <EmptyState title="No site matches" action={<Button onClick={() => (setQ(""), setFilter("all"))}>Show all sites</Button>}>
            {q
              ? "Nothing matches “" + q + "”" + (filter !== "all" ? " among " + label(filter).toLowerCase() + " sites." : ".")
              : "None of your sites is " + label(filter).toLowerCase() + " right now."}
          </EmptyState>
        }
      />
    </section>
  );
}

/** Deleted is the bin: sites deleted in the last 7 days, each with Restore. */
function Deleted({ me }: { me: Me }) {
  const bin = useSites(true);
  const call = useApi();
  const qc = useQueryClient();
  const toast = useToast();
  const [busy, setBusy] = useState("");
  const list = bin.data || [];
  if (!list.length) return null;
  const restore = async (s: SiteItem) => {
    setBusy(s.name);
    try {
      await call("PATCH", sitePath(s, me), { restore: true });
      await qc.invalidateQueries({ queryKey: ["sites"] });
      toast("Restored " + s.name + ", with its saved data.");
    } catch (e) {
      toast((e as Error).message, { bad: true });
    } finally {
      setBusy("");
    }
  };
  return (
    <div id="deleted" className="mt-12">
      <Part title="Recently deleted" note="Deleted sites are kept for 7 days with their saved data, then removed for good. Their names stay yours until then.">
        <ul className="m-0 list-none p-0">
          {list.map((s) => (
            <Row key={s.owner + "/" + s.name} end={<Button onClick={() => restore(s)} disabled={busy === s.name}>Restore</Button>}>
              <p className="font-semibold">{s.name}</p>
              <p className="text-sm text-ink-soft">
                Deleted {dateOf(s.deleted_at)} · removed for good on {dateOf(new Date(t(s.deleted_at || "") + 7 * 86400000).toISOString())}
              </p>
            </Row>
          ))}
        </ul>
      </Part>
    </div>
  );
}

/** Invitations are teams that asked me to join; joining shows their sites here. */
function Invitations({ me }: { me: Me }) {
  const call = useApi();
  const qc = useQueryClient();
  const toast = useToast();
  return (
    <>
      {me.invitations.map((i) => (
        <Notice
          key={i.id}
          title={"The team " + (i.display_name || i.team) + " invited you"}
          action={
            <Button
              onClick={async () => {
                try {
                  await call("POST", "/v1/invites/" + encodeURIComponent(i.id) + "/accept", {});
                  await qc.invalidateQueries();
                  toast("You joined " + (i.display_name || i.team) + ". Its sites are in your list.");
                } catch (e) {
                  toast((e as Error).message, { bad: true });
                }
              }}
            >
              Join {i.display_name || i.team}
            </Button>
          }
        >
          Members manage the team's sites with their own sign-in.
        </Notice>
      ))}
    </>
  );
}

/** FirstHandle is a new account's one step: the name every site address is under. */
function FirstHandle({ me }: { me: Me }) {
  const call = useApi();
  const qc = useQueryClient();
  const guess = me.email.split("@")[0].toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 40);
  const [handle, setHandle] = useState(guess);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  return (
    <div className="flex flex-col items-center pt-8 pb-12">
      <div className="w-full max-w-[460px]">
        <Invitations me={me} />
      </div>
      <Card className="max-w-[460px]">
        <CardTitle>Choose your name</CardTitle>
        <CardLead>Every site you make lives at an address under it. You can change it later, but old links then stop working.</CardLead>
        <form
          noValidate
          onSubmit={async (e) => {
            e.preventDefault();
            const h = handle.trim().toLowerCase();
            if (!validName(h)) return setError("Use lower-case letters, digits and single hyphens, like ana or ana-bakes.");
            setBusy(true);
            setError("");
            try {
              await call("PATCH", "/v1/owners/" + encodeURIComponent(me.id), { handle: h });
              await qc.invalidateQueries({ queryKey: ["me"] });
            } catch (err) {
              setError((err as Error).message);
            } finally {
              setBusy(false);
            }
          }}
        >
          <Field id="handle" label="Your name on Simple Host" error={error} hint="Lower-case letters, digits and single hyphens. It is part of every site's address.">
            <Input
              id="handle"
              value={handle}
              onChange={(e) => setHandle(e.target.value)}
              autoComplete="username"
              autoCapitalize="none"
              spellCheck={false}
              maxLength={40}
              aria-invalid={!!error}
              aria-describedby="handle-msg"
            />
          </Field>
          <Button type="submit" variant="primary" size="lg" disabled={busy}>
            {busy ? "Saving…" : "Continue"}
          </Button>
        </form>
        <p className="mt-4 text-sm text-ink-faint">Signed in as {me.email}.</p>
      </Card>
    </div>
  );
}
