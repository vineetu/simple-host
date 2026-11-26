// Website (/e/{slug}/team?s=website): getting the team site published.
// A two-part state strip (AI app, Website), the team site once it is live,
// then "Publish from your AI app" (a tab per app with the numbered connect
// steps, the connector address, and a ready prompt), "Or upload a zip", and
// the command line behind Advanced. Once published the guide folds away and
// Notifications and Keep a copy appear. While nothing is published the page
// checks again every 5 seconds for 10 minutes. After the deadline, or once
// the site is taken down, publishing is closed.
import { useEffect, useRef, useState, type ReactNode } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Badge } from "@kit/components/ui/badge";
import { Button } from "@kit/components/ui/button";
import { CopyButton } from "@kit/components/ui/copy";
import { EmptyState } from "@kit/components/ui/empty-state";
import { FieldMessage, Input } from "@kit/components/ui/input";
import { Notice } from "@kit/components/ui/notice";
import { PageTitle } from "@kit/components/ui/page-shell";
import { Skeleton } from "@kit/components/ui/skeleton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@kit/components/ui/tabs";
import { useToast } from "@kit/components/ui/toast";
import { cn } from "@kit/lib/utils";
import { paths, useHack } from "@/lib/client";
import { safeHref } from "@/lib/labels";
import { href } from "@/routes/resolve";
import { useEventDetail, useMe } from "@/app/data";
import { AppLink } from "@/app/nav";
import { useBytes } from "@/app/Providers";
import type { ScreenProps } from "@/routes/registry";
import connectSteps from "../../../../../products/hack/webassets/static/hack-connect-steps.json";
import type { EntryData } from "../entry/data";
import { evOf, teamOf, type MyTeam } from "../team/data";
import { say, signedOut } from "../team/words";
import { POLL_LIMIT_MS, POLL_MS, archiveError, connectedTo, fill, hostOnly, joinedApps, prompt, siteLine, strip, type Cell, type SiteStatus, type Steps } from "./data";
import { Hooks } from "./Hooks";
import { putBytes, save } from "./upload";

const STEPS = connectSteps as Steps;
const DEFAULT_APP = "claude";

const Title = () => <PageTitle>Website</PageTitle>;

export default function Website({ match }: ScreenProps) {
  const slug = match.slug!;
  const d = useEventDetail(slug).data!;
  const team = teamOf(d);
  if (!team)
    return (
      <>
        <Title />
        <EmptyState
          title="Join or start a team first"
          action={
            <Button asChild>
              <AppLink to={href("participant", slug, "team")}>Go to Team</AppLink>
            </Button>
          }
        />
      </>
    );
  return <Publish slug={slug} team={team} />;
}

function Publish({ slug, team }: { slug: string; team: MyTeam }) {
  const api = useHack();
  const qc = useQueryClient();
  const toast = useToast();
  const d = useEventDetail(slug).data!;
  const ev = evOf(d);
  const frozen = typeof team.frozen === "boolean" ? team.frozen : ev.stage === "closed";
  const ended = ev.stage === "archived";
  // Polling: from the first answer, for 10 minutes, only while the tab is visible (TanStack pauses in the background).
  const started = useRef(0);
  const [expired, setExpired] = useState(false);
  const q = useQuery({
    queryKey: ["team-site", slug],
    queryFn: () => api.team.site(slug) as Promise<SiteStatus>,
    refetchInterval: (query) => {
      const s = query.state.data?.site || {};
      const closed = !!(s.taken_down || frozen || ended);
      if (!query.state.data || s.exists || closed || expired) return false;
      started.current = started.current || Date.now();
      if (Date.now() - started.current > POLL_LIMIT_MS) {
        setTimeout(() => setExpired(true));
        return false;
      }
      return POLL_MS;
    },
  });
  // The entry fills the prompt; when it does not load the prompt is just shorter.
  const entry = useQuery({ queryKey: ["entry", slug], queryFn: () => api.entries.get(slug) as Promise<EntryData>, retry: false });

  // A change this page saw itself (by polling, or after an action) is said once; the first answer never is.
  const before = useRef<{ conn: string; published: boolean } | null>(null);
  const data = q.data;
  useEffect(() => {
    if (!data) return;
    const conn = data.connection || { state: "none" };
    const s = data.site || {};
    const was = before.current;
    if (was && was.conn !== "connected" && conn.state === "connected") toast("Connected in " + joinedApps(conn) + ".");
    if (was && !was.published && s.exists) toast("Published" + (typeof s.live_version === "number" ? " · version " + s.live_version : "") + ".");
    before.current = { conn: conn.state, published: !!s.exists };
  }, [data, toast]);

  if (q.isPending)
    return (
      <>
        <Title />
        <div className="mt-6" role="status" aria-busy="true" aria-label="Loading your team’s site">
          <Skeleton className="h-24 w-full" />
          <Skeleton className="mt-5 h-72 w-full" />
        </div>
      </>
    );
  if (q.isError)
    return (
      <>
        <Title />
        <Notice tone="bad" title="Could not load your team’s site." action={<Button onClick={() => q.refetch()}>Try again</Button>} />
      </>
    );
  const site = data!.site || {};
  const conn = data!.connection || { state: "none", teammates_connected: [] };
  const closed = !!(site.taken_down || frozen || ended);
  const s = strip(site, conn, closed);
  const polling = !site.exists && !closed;
  const link = safeHref(site.url);

  return (
    <>
      <Title />
      <div className="mt-5 grid gap-3 md:grid-cols-2" aria-label="Where things stand" role="group">
        <StripCell k="AI app" c={s.app} />
        <StripCell
          k="Website"
          c={s.web}
          extra={
            polling && expired ? (
              <Button
                className="mt-2"
                onClick={() => {
                  started.current = Date.now();
                  setExpired(false);
                  void q.refetch();
                }}
              >
                Check again
              </Button>
            ) : undefined
          }
        />
      </div>

      {site.exists ? (
        <Box title="Your team site">
          <div className="mt-2 flex flex-wrap items-center gap-2">
            {site.taken_down ? <Badge tone="down">Taken down</Badge> : <Badge tone="ok">Live</Badge>}
            {link ? (
              <a href={link} target="_blank" rel="noopener" className="inline-flex min-h-11 min-w-0 items-center break-all">
                {hostOnly(site.url)}
              </a>
            ) : (
              <span className="break-all">{hostOnly(site.url)}</span>
            )}
          </div>
          <p className="mt-1 text-sm text-ink-soft">{siteLine(site)}</p>
          {link && (
            <div className="mt-3">
              <Button asChild>
                <a href={link} target="_blank" rel="noopener">
                  Open website <span aria-hidden="true">↗</span>
                  <span className="sr-only"> (opens in a new tab)</span>
                </a>
              </Button>
            </div>
          )}
        </Box>
      ) : !site.ready ? (
        <Notice title="Your team’s address is being set up. It is usually ready within a few minutes." />
      ) : closed ? (
        <Notice title="Nothing was published by the deadline." />
      ) : null}

      {closed ? (
        <Notice title={"Publishing is closed." + (site.exists ? " Your site stays live as submitted." : "")} />
      ) : (
        <>
          <Guide conn={conn} published={!!site.exists} mcp={data!.mcp_url || location.origin + "/mcp"} promptText={prompt(ev.title, team.name, entry.data?.entry)} />
          <Zip slug={slug} team={team} limits={data!.upload_limits || ""} onDone={() => qc.invalidateQueries({ queryKey: ["team-site", slug] })} />
          <details className="mt-5 rounded-wobble border border-rule bg-card px-4 py-1 md:px-5">
            <summary className="flex min-h-11 cursor-pointer items-center type-section">Advanced: command line and API key</summary>
            <div className="pt-1 pb-4">
              <p>Publish from your command line with your own key (the Keys panel on your account page makes one).</p>
              <p className="mt-2 font-mono text-sm break-all">
                {'curl -X PUT "' +
                  (data!.api_base || location.origin) +
                  paths.siteUpload(team.slug, slug) +
                  '" -H "X-API-Key: YOUR_KEY" -H "Content-Type: application/zip" --data-binary @site.zip'}
              </p>
            </div>
          </details>
        </>
      )}

      {site.exists && <Hooks slug={slug} team={team.slug} />}
      {site.exists && <KeepCopy slug={slug} team={team.slug} />}
      <div className="pb-14" />
    </>
  );
}

function StripCell({ k, c, extra }: { k: string; c: Cell; extra?: ReactNode }) {
  const link = safeHref(c.link);
  return (
    <div className={cn("min-w-0 rounded-wobble border-[1.5px] bg-card px-4 py-3", c.on === "on" ? "border-good" : c.on === "wait" ? "border-dashed border-ink-soft" : "border-rule")}>
      <p className="text-xs text-ink-soft">{k}</p>
      <p className={cn("font-semibold", c.on === "on" && "text-good")}>{c.label}</p>
      {(c.sub || link) && (
        <p className="text-sm break-words text-ink-soft">
          {c.sub}
          {link && (
            <>
              {c.sub ? " · " : ""}
              <a href={link} target="_blank" rel="noopener">
                Open {hostOnly(c.link)} <span aria-hidden="true">↗</span>
              </a>
            </>
          )}
        </p>
      )}
      {extra}
    </div>
  );
}

export function Box({ title, sub, children }: { title: string; sub?: ReactNode; children?: ReactNode }) {
  return (
    <section className="mt-5 rounded-wobble border border-rule bg-card px-4 py-4 md:px-5">
      <h2 className="type-section">{title}</h2>
      {sub && <p className="mt-0.5 text-sm text-ink-soft">{sub}</p>}
      {children}
    </section>
  );
}

function CopyRow({ value, label, button = "Copy" }: { value: string; label: string; button?: string }) {
  return (
    <div className="mt-2 flex flex-wrap items-center gap-2">
      <Input readOnly value={value} aria-label={label} className="min-w-0 flex-[1_1_14rem] font-mono text-sm" onFocus={(e) => e.target.select()} />
      <CopyButton text={value} label={button} />
    </div>
  );
}

function Guide({ conn, published, mcp, promptText }: { conn: NonNullable<SiteStatus["connection"]>; published: boolean; mcp: string; promptText: string }) {
  const me = useMe().data;
  const email = String(me?.login || me?.email || me?.username || "");
  const all = STEPS.apps || [];
  const list = all.filter((a) => a.tab);
  const others = all.filter((a) => !a.tab);
  const ids = [...list.map((a) => a.id), "other"];
  const wanted = (conn.app || "").toLowerCase();
  const [tab, setTab] = useState(ids.includes(wanted) ? wanted : DEFAULT_APP);
  const app = list.find((a) => a.id === tab);
  const hosted = location.origin === "https://simple-hack.app";

  const tabs = (body: ReactNode) => (
    <Tabs value={tab} onValueChange={setTab} className="mt-3">
      <TabsList aria-label="How you publish">
        {list.map((a) => (
          <TabsTrigger key={a.id} value={a.id} onClick={() => setTab(a.id)}>
            {a.label}
          </TabsTrigger>
        ))}
        <TabsTrigger value="other" onClick={() => setTab("other")}>
          Other AI apps
        </TabsTrigger>
      </TabsList>
      <TabsContent value={tab} className="pt-0 pb-0">
        {body}
      </TabsContent>
    </Tabs>
  );

  let body: ReactNode;
  if (tab === "other")
    body = (
      <>
        <div className="mt-4 flex flex-wrap gap-2">
          {others.map((a) => (
            <Button key={a.id} asChild>
              <a href={"/get-started#" + a.id} target="_blank" rel="noopener">
                {a.label}
              </a>
            </Button>
          ))}
        </div>
        <p className="mt-2 text-sm text-ink-soft">Add Simple Hack to that app, then come back here and describe your idea in a new chat.</p>
      </>
    );
  else if (!app)
    body = (
      <p className="mt-3 text-sm text-ink-soft">
        Steps for this app are not available right now. <a href="/get-started">See the full setup guide</a>.
      </p>
    );
  else if (published) body = <p className="mt-3 text-sm text-ink-soft">Ask {app.label} to change the site and publish again. Each publish makes a new version; the newest is live.</p>;
  else {
    const done = connectedTo(conn, app.label);
    const connect = (hosted && app.connect_hosted) || app.connect || [];
    let addressUsed = false; // one address row on the screen
    const address = () => {
      if (addressUsed) return null;
      addressUsed = true;
      return <CopyRow value={mcp} label="Connector address" />;
    };
    const hint = email && !email.includes("@") ? STEPS.signin_hint_username : STEPS.signin_hint;
    const items: { html: string; done: boolean; extra: ReactNode }[] = connect.map((step, n) => ({
      html: step.html + (step.after || ""),
      done,
      extra: (
        <>
          {step.address && address()}
          {step.command && <CopyRow value={step.command.split("{address}").join(mcp)} label="Command" />}
          {step.copy && <CopyRow value={step.copy.value} label={step.copy.label} />}
          {n === connect.length - 1 && (
            <>
              {hosted && app.fallback && (
                <>
                  <p className="mt-2 text-sm text-ink-soft" dangerouslySetInnerHTML={{ __html: app.fallback }} />
                  {address()}
                </>
              )}
              {hint && <p className="mt-2 text-sm text-ink-soft" dangerouslySetInnerHTML={{ __html: fill(hint, app.label, email) }} />}
            </>
          )}
        </>
      ),
    }));
    for (const step of STEPS.publish || [])
      items.push({
        html: fill(step.html, app.label, email),
        done: false,
        extra: step.prompt ? (
          <>
            <p className="mt-2 rounded-field border border-rule bg-paper px-3 py-2 text-sm break-words whitespace-pre-line">{promptText}</p>
            <div className="mt-2">
              <CopyButton text={promptText} label="Copy prompt" />
            </div>
          </>
        ) : null,
      });
    body = (
      <ol className="m-0 mt-4 list-none p-0">
        {items.map((it, i) => (
          <li key={i} className="flex gap-3 border-b border-rule py-3 last:border-b-0">
            <span aria-hidden="true" className={cn("grid size-7 flex-none place-items-center rounded-tag border-[1.5px] text-sm", it.done ? "border-good bg-good text-on-accent" : "border-ink-soft text-ink-soft")}>
              {it.done ? "✓" : i + 1}
            </span>
            <div className="min-w-0 flex-1">
              {it.done && <span className="sr-only">Done: </span>}
              <span dangerouslySetInnerHTML={{ __html: it.html }} />
              {it.extra}
            </div>
          </li>
        ))}
      </ol>
    );
  }

  if (published)
    return (
      <details className="mt-5 rounded-wobble border border-rule bg-card px-4 py-1 md:px-5">
        <summary className="flex min-h-11 cursor-pointer items-center type-section">Publish again, or from another AI app</summary>
        <div className="pb-4">
          {tabs(body)}
        </div>
      </details>
    );
  return (
    <Box
      title="Publish from your AI app"
      sub={
        tab !== "other" && app && conn.state === "connected"
          ? "You're connected. Paste the prompt into " + app.label + " and it publishes here."
          : tab !== "other" && app
            ? "Describe your idea in " + app.label + ". It builds the page and publishes it here. No key, no code."
            : "Describe your idea in the AI app you already use. It builds the page and publishes it here. No key, no code."
      }
    >
      {tabs(body)}
    </Box>
  );
}

function Zip({ slug, team, limits, onDone }: { slug: string; team: MyTeam; limits: string; onDone: () => void }) {
  const input = useRef<HTMLInputElement>(null);
  const [status, setStatus] = useState<{ text: string; bad?: boolean } | null>(null);
  const [busy, setBusy] = useState(false);
  return (
    <Box title="Or upload a zip" sub={"Static files only, index.html at the root. " + limits}>
      <form
        className="mt-3 flex flex-wrap items-center gap-2"
        onSubmit={async (e) => {
          e.preventDefault();
          const file = input.current?.files?.[0];
          if (!file) return setStatus({ text: "Choose a zip first.", bad: true });
          setBusy(true);
          setStatus({ text: "Uploading and publishing…" });
          try {
            const r = await putBytes<{ active_version?: number }>("PUT", paths.siteUpload(team.slug, slug), file, "application/zip");
            setStatus({ text: "Published version " + r.active_version + "." });
            if (input.current) input.current.value = "";
            onDone();
          } catch (err) {
            if (signedOut(err)) return;
            setStatus({ text: (err as { code?: string }).code === "offline" ? "Could not confirm the upload. Check your site before trying again." : archiveError(err, "Could not publish your site."), bad: true });
          } finally {
            setBusy(false);
          }
        }}
      >
        <input
          ref={input}
          id="web-zip"
          type="file"
          accept=".zip"
          aria-label="Your site archive"
          aria-describedby="web-zip-msg"
          className="block min-h-11 min-w-0 flex-[1_1_14rem] text-sm file:mr-3 file:min-h-11 file:cursor-pointer file:rounded-wobble file:border-[1.5px] file:border-ink-soft file:bg-card file:px-4 file:text-base file:text-ink"
        />
        <Button type="submit" disabled={busy}>
          Upload
        </Button>
      </form>
      <FieldMessage tone={status?.bad ? "bad" : undefined} id="web-zip-msg">
        {status?.text}
      </FieldMessage>
    </Box>
  );
}

function KeepCopy({ slug, team }: { slug: string; team: string }) {
  const bytes = useBytes();
  const toast = useToast();
  const [busy, setBusy] = useState(false);
  return (
    <Box title="Keep a copy" sub="Download your published files.">
      <div className="mt-3">
        <Button
          disabled={busy}
          onClick={async () => {
            setBusy(true);
            try {
              save(await bytes(paths.teamExport(slug)), slug + "-" + team + "-site.tar.gz");
            } catch (err) {
              if (!signedOut(err)) toast(say(err, "Could not download the file."), { bad: true });
            } finally {
              setBusy(false);
            }
          }}
        >
          Download our team site
        </Button>
      </div>
    </Box>
  );
}
