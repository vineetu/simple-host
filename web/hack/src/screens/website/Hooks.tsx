// Notifications (webhooks) for the team's site: a signed message when a
// visitor saves something or uploads a file. Each one lists its deliveries,
// and the person can send a test, pause or resume it, rotate its signing
// secret or remove it; a new one is added with an https address. The secret
// is shown once, when it is made or rotated.
import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Button } from "@kit/components/ui/button";
import { CopyButton } from "@kit/components/ui/copy";
import { AskDialog } from "@kit/components/ui/dialog";
import { Field, Input } from "@kit/components/ui/input";
import { Notice } from "@kit/components/ui/notice";
import { useToast } from "@kit/components/ui/toast";
import { teamSite } from "@/lib/client/webhooks";
import { useHack } from "@/lib/client";
import { dateTime } from "@/lib/time";
import { say, signedOut } from "../team/words";
import { EVENT_LABEL, NOTIFY_EVENTS, deliveryLine, hookLine, receiver, testLine, type Delivery, type Hook, type HookList } from "./data";

type Secret = { secret: string; action: "added" | "rotated"; previous?: string };

export function Hooks({ slug, team }: { slug: string; team: string }) {
  const api = useHack();
  const qc = useQueryClient();
  const toast = useToast();
  const h = teamSite(team, slug);
  const key = ["team-hooks", slug, team];
  const q = useQuery({ queryKey: key, queryFn: () => api.webhooks.list(h) as Promise<HookList> });
  const [secret, setSecret] = useState<Secret | null>(null);
  const [ask, setAsk] = useState<null | { kind: "off" | "rotate"; n: Hook }>(null);
  const [busy, setBusy] = useState("");
  const [open, setOpen] = useState<Record<string, boolean>>({});
  // The add form.
  const [url, setUrl] = useState("");
  const [urlErr, setUrlErr] = useState("");
  const [storage, setStorage] = useState("");
  const [label, setLabel] = useState("");
  const [hName, setHName] = useState("");
  const [hValue, setHValue] = useState("");

  // Left out when it does not load, or when the server has none and cannot make any.
  if (!q.data) return null;
  const limits = q.data.limits || {};
  const items = q.data.notifications || [];
  if (limits.enabled === false && !items.length) return null;
  const canManage = limits.can_manage !== false;
  const reload = () => qc.invalidateQueries({ queryKey: key });
  const run = async (id: string, fn: () => Promise<unknown>) => {
    setBusy(id);
    try {
      await fn();
      return true;
    } catch (err) {
      if (!signedOut(err)) toast(say(err), { bad: true });
      return false;
    } finally {
      setBusy("");
    }
  };

  return (
    <details className="mt-5 rounded-wobble border border-rule bg-card px-4 py-1 md:px-5">
      <summary className="flex min-h-11 cursor-pointer items-center type-section">Notifications</summary>
      <div className="pb-4">
        <p>Get a signed message when a visitor saves something or uploads a file on your team’s site.</p>
        {items.length > 0 && (
          <ul className="m-0 mt-3 list-none rounded-wobble border border-rule px-3 py-1" aria-label="Notifications">
            {items.map((n) => (
              <li key={n.id} className="border-b border-rule py-2 last:border-b-0">
                <p className="font-medium break-words">{receiver(n)}</p>
                <p className="text-sm break-words text-ink-soft">{hookLine(n)}</p>
                <div className="mt-2 flex flex-wrap gap-2">
                  <Button aria-expanded={!!open[n.id]} onClick={() => setOpen((o) => ({ ...o, [n.id]: !o[n.id] }))}>
                    {open[n.id] ? "Hide deliveries" : "Deliveries"}
                  </Button>
                  {canManage && n.source === "owner" && n.state !== "expired" && (
                    <Button
                      disabled={busy === "t" + n.id}
                      onClick={async () => {
                        setBusy("t" + n.id);
                        try {
                          const r = (await api.webhooks.test(h, n.id)) as { delivered?: boolean; result?: string; status?: number };
                          toast(testLine(r), { bad: !r.delivered });
                        } catch (err) {
                          if (!signedOut(err)) toast(say(err), { bad: true });
                        } finally {
                          setBusy("");
                        }
                      }}
                    >
                      Send test
                    </Button>
                  )}
                  {canManage && (
                    <Button
                      disabled={busy === "p" + n.id}
                      onClick={async () => {
                        const paused = !(n.state === "paused" || n.state === "suspended");
                        if (await run("p" + n.id, () => api.webhooks.update(h, n.id, { paused }))) {
                          setSecret(null);
                          await reload();
                        }
                      }}
                    >
                      {n.state === "paused" || n.state === "suspended" ? "Resume" : "Pause"}
                    </Button>
                  )}
                  {canManage && n.source === "owner" && <Button onClick={() => setAsk({ kind: "rotate", n })}>Rotate secret</Button>}
                  <Button variant="danger" onClick={() => setAsk({ kind: "off", n })}>
                    Remove
                  </Button>
                </div>
                {open[n.id] && <Deliveries id={n.id} team={team} slug={slug} />}
              </li>
            ))}
          </ul>
        )}
        {secret && (
          <div className="mt-3 rounded-field border-[1.5px] border-good bg-card px-4 py-3" role="status">
            <p>
              {secret.action === "rotated" ? "Rotated." : "Added."} Signing secret (shown once): <span className="font-mono break-all">{secret.secret}</span>
              {secret.previous ? " The previous secret keeps working until " + dateTime(secret.previous) + "." : ""}
            </p>
            <div className="mt-2">
              <CopyButton text={secret.secret} />
            </div>
          </div>
        )}
        {limits.enabled === false ? (
          <p className="mt-3 text-sm text-ink-soft">Notifications are unavailable on this server.</p>
        ) : limits.can_create === false ? (
          <p className="mt-3 text-sm text-ink-soft">New subscriptions are closed. Existing subscriptions continue after the submission deadline.</p>
        ) : (
          <form
            className="mt-4"
            onSubmit={async (e) => {
              e.preventDefault();
              const u = url.trim();
              if (!/^https:\/\//i.test(u)) return setUrlErr("Use an https URL.");
              setUrlErr("");
              const body: Record<string, unknown> = { url: u, events: NOTIFY_EVENTS, storage: storage.trim(), label: label.trim() };
              if (hName.trim() || hValue) body.auth_header = { name: hName.trim(), value: hValue };
              setBusy("add");
              try {
                const r = (await api.webhooks.create(h, body)) as { secret?: string };
                setSecret(r.secret ? { secret: r.secret, action: "added" } : null);
                setUrl("");
                setStorage("");
                setLabel("");
                setHName("");
                setHValue("");
                await reload();
              } catch (err) {
                if (!signedOut(err)) toast(say(err), { bad: true });
              } finally {
                setBusy("");
              }
            }}
          >
            <Field id="notif-url" label="Notification URL" error={urlErr}>
              <Input id="notif-url" type="url" placeholder="https://example.com/notify" value={url} aria-invalid={!!urlErr || undefined} aria-describedby="notif-url-msg" onChange={(e) => setUrl(e.target.value)} />
            </Field>
            <Field id="notif-storage" label="Storage (optional)">
              <Input id="notif-storage" placeholder="all" value={storage} onChange={(e) => setStorage(e.target.value)} />
            </Field>
            <Field id="notif-label" label="Label (optional)">
              <Input id="notif-label" value={label} onChange={(e) => setLabel(e.target.value)} />
            </Field>
            <details className="mb-4">
              <summary className="flex min-h-11 cursor-pointer items-center text-ink-soft">Advanced: authentication header</summary>
              <div className="pt-2">
                <Field id="notif-header-name" label="Header name (optional)">
                  <Input id="notif-header-name" maxLength={64} value={hName} onChange={(e) => setHName(e.target.value)} />
                </Field>
                <Field id="notif-header-value" label="Header value">
                  <Input id="notif-header-value" type="password" maxLength={1024} value={hValue} onChange={(e) => setHValue(e.target.value)} />
                </Field>
              </div>
            </details>
            <Button type="submit" disabled={busy === "add"}>
              Add notification
            </Button>
          </form>
        )}
      </div>
      <AskDialog
        open={ask?.kind === "off"}
        onOpenChange={(o) => !o && setAsk(null)}
        title="Turn off this notification?"
        body={"Stop sending notifications to " + (ask ? receiver(ask.n) : "") + "."}
        confirmLabel="Turn off"
        danger
        onConfirm={async () => {
          if (ask && (await run("o", () => api.webhooks.remove(h, ask.n.id)))) {
            setSecret(null);
            await reload();
          }
        }}
      />
      <AskDialog
        open={ask?.kind === "rotate"}
        onOpenChange={(o) => !o && setAsk(null)}
        title="Rotate the signing secret?"
        body="A new secret replaces it. The old one keeps working for a short overlap, then stops."
        confirmLabel="Rotate secret"
        onConfirm={async () => {
          if (!ask) return;
          try {
            const r = (await api.webhooks.rotateSecret(h, ask.n.id)) as { secret?: string; previous_valid_until?: string };
            setSecret({ secret: r.secret || "", action: "rotated", previous: r.previous_valid_until || "" });
            await reload();
          } catch (err) {
            if (!signedOut(err)) toast(say(err), { bad: true });
          }
        }}
      />
    </details>
  );
}

function Deliveries({ id, team, slug }: { id: string; team: string; slug: string }) {
  const api = useHack();
  const q = useQuery({
    queryKey: ["team-hook-deliveries", slug, team, id],
    queryFn: () => api.webhooks.deliveries(teamSite(team, slug), id).then((r) => ((r as { deliveries?: Delivery[] }).deliveries || []) as Delivery[]),
  });
  if (q.isPending) return <p className="mt-2 text-sm text-ink-soft">Loading…</p>;
  if (q.isError) return <Notice tone="bad" title={say(q.error)} />;
  if (!q.data.length) return <p className="mt-2 text-sm text-ink-soft">No deliveries in this period.</p>;
  return (
    <ul className="m-0 mt-2 list-none p-0" aria-label="Deliveries">
      {q.data.map((v, i) => (
        <li key={i} className="py-1 text-sm">
          <span className="font-medium">{EVENT_LABEL[v.name] || v.name}</span> <span className="text-ink-soft">{deliveryLine(v)}</span>
        </li>
      ))}
    </ul>
  );
}
