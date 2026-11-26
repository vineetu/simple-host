// Connected AI apps (Account): every app connected to the account, most
// recently used first, with Disconnect; under them the same per-app connect
// steps as /get-started (hack-connect-steps.json), in a disclosure that is
// open while nothing is connected (account.js loadConnectCard).
import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Button } from "@kit/components/ui/button";
import { CopyButton } from "@kit/components/ui/copy";
import { FieldMessage, Input } from "@kit/components/ui/input";
import { Section } from "@kit/components/ui/page-shell";
import { Skeleton } from "@kit/components/ui/skeleton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@kit/components/ui/tabs";
import { useHack } from "@/lib/client";
import type { ConnectApp, ConnectStep } from "@/lib/client/hack";
import { accountErr, connectionRows, site, stepsFor, type Words } from "./data";
import type { Ask } from "./Ask";

function CopyRow({ value, label }: { value: string; label: string }) {
  return (
    <div className="mt-2 flex gap-2">
      <Input readOnly value={value} aria-label={label} className="h-11 font-mono text-sm" />
      <CopyButton text={value} />
    </div>
  );
}

function Step({ s, address }: { s: ConnectStep & { fallback?: string }; address: string }) {
  return (
    <li className="mb-3 pl-1">
      {/* hack-connect-steps.json's html is our own markup. */}
      <span dangerouslySetInnerHTML={{ __html: s.html + (s.after || "") }} />
      {s.address && <CopyRow value={address} label="Connector address" />}
      {s.command && <CopyRow value={s.command.split("{address}").join(address)} label="Command" />}
      {s.copy && <CopyRow value={s.copy.value} label={s.copy.label} />}
      {s.fallback && (
        <>
          <p className="mt-2 text-sm text-ink-soft">{s.fallback}</p>
          <CopyRow value={address} label="Connector address" />
        </>
      )}
    </li>
  );
}

function Steps() {
  const api = useHack();
  const q = useQuery({ queryKey: ["connect-steps"], queryFn: () => api.hack.connectSteps().catch(() => null), staleTime: Infinity });
  const [tab, setTab] = useState("");
  if (q.isPending) return <Skeleton className="mt-3 h-24 w-full" />;
  const all: ConnectApp[] = q.data?.apps || [];
  if (!all.length)
    return (
      <p className="mt-3 text-sm text-ink-soft">
        Steps for this app are not available right now. <a href="/get-started">See the full setup guide</a>.
      </p>
    );
  const address = site.origin() + "/mcp";
  const tabs = all.filter((a) => a.tab);
  const open = tab || tabs[0]?.id || "other";
  return (
    <Tabs value={open} onValueChange={setTab} className="mt-2">
      <TabsList aria-label="How you connect">
        {tabs.map((a) => (
          <TabsTrigger key={a.id} value={a.id}>
            {a.label}
          </TabsTrigger>
        ))}
        <TabsTrigger value="other">Other AI apps</TabsTrigger>
      </TabsList>
      {tabs.map((a) => (
        <TabsContent key={a.id} value={a.id} className="pb-2">
          <ol className="m-0 list-decimal pl-6">
            {stepsFor(a, site.origin()).map((s, i) => (
              <Step key={i} s={s} address={address} />
            ))}
          </ol>
        </TabsContent>
      ))}
      <TabsContent value="other" className="pb-2">
        <div className="flex flex-wrap gap-2">
          {all
            .filter((a) => !a.tab)
            .map((a) => (
              <Button key={a.id} asChild>
                <a href={"/get-started#" + a.id} target="_blank" rel="noopener">
                  {a.label}
                  <span className="sr-only"> (opens in a new tab)</span>
                </a>
              </Button>
            ))}
        </div>
      </TabsContent>
    </Tabs>
  );
}

export function ConnectApps({ w, ask }: { w: Words; ask: Ask }) {
  const api = useHack();
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["connections"], queryFn: () => api.me.connectionList() });
  const [userOpen, setUserOpen] = useState<boolean | null>(null);
  const rows = q.data ? connectionRows(q.data) : [];
  const state = q.isPending ? "loading" : q.isError ? "error" : rows.length ? "connected" : "none";
  const open = userOpen ?? (state === "none" || state === "error");

  return (
    <Section title="Connected AI apps">
      {state === "loading" && (
        <div role="status" aria-busy="true" aria-label="Loading connected apps">
          <Skeleton className="h-14 w-full" />
        </div>
      )}
      {state === "error" && (
        <div className="flex flex-wrap items-center gap-3">
          <FieldMessage tone="bad">{accountErr(q.error, w, "Could not load connected apps")}</FieldMessage>
          <Button onClick={() => q.refetch()}>Try again</Button>
        </div>
      )}
      {state === "none" && <p className="text-ink-soft">None yet.</p>}
      {state === "connected" && (
        <ul aria-label="Connected AI apps" className="m-0 list-none rounded-wobble border border-rule bg-card px-3 py-1">
          {rows.map(({ c, name, line }) => (
            <li key={c.client_id} className="flex min-h-14 flex-wrap items-center gap-3 border-b border-rule px-1 py-2 last:border-b-0">
              <span className="min-w-0 flex-[1_1_14rem]">
                <span className="block font-semibold">{name}</span>
                <span className="block text-sm text-ink-soft">{line}</span>
              </span>
              <Button
                variant="danger"
                onClick={() =>
                  ask({
                    title: "Disconnect " + name + "?",
                    confirmLabel: "Disconnect",
                    danger: true,
                    run: async () => {
                      await api.me.removeConnection(c.client_id);
                      await qc.invalidateQueries({ queryKey: ["connections"] });
                    },
                    failed: "Could not disconnect app",
                  })
                }
              >
                Disconnect
              </Button>
            </li>
          ))}
        </ul>
      )}
      <details open={open} onToggle={(e) => setUserOpen((e.target as HTMLDetailsElement).open)} className="mt-4">
        <summary className="flex min-h-11 cursor-pointer items-center font-medium text-accent">
          {state === "connected" ? "Connect another app" : "Connect an AI app"}
        </summary>
        {open && <Steps />}
      </details>
    </Section>
  );
}
