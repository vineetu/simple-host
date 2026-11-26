// Settings → Notifications: webhooks for the event's own website (a visitor
// saves something or uploads a file). Only once the event runs its own
// website; otherwise the tab says where that is set up.
import { useState, type ReactNode } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { AskDialog, Dialog, DialogActions } from "@kit/components/ui/dialog";
import { Badge } from "@kit/components/ui/badge";
import { Button } from "@kit/components/ui/button";
import { Check } from "@kit/components/ui/choice";
import { CopyButton } from "@kit/components/ui/copy";
import { EmptyState } from "@kit/components/ui/empty-state";
import { Field, FieldMessage, Input } from "@kit/components/ui/input";
import { Notice } from "@kit/components/ui/notice";
import { useToast } from "@kit/components/ui/toast";
import { useHack } from "@/lib/client";
import { eventSite } from "@/lib/client/webhooks";
import { dateTime } from "@/lib/time";
import { href } from "@/routes/resolve";
import { AppLink } from "@/app/nav";
import { Failed } from "@/app/states";
import type { TabProps } from "./Settings";
import { errorText, Panel } from "./common";
import { eventName, NOTIFY_DELIVERY_STATES, NOTIFY_EVENTS, NOTIFY_RESULTS, NOTIFY_STATES, receiverName, testLine, type Hook } from "./settings";
import { locks, TabLoading } from "./settings-ui";
import { LockNote } from "@/shared/locks";

type List = { notifications?: Hook[]; limits?: { can_create?: boolean; can_manage?: boolean; history_days?: number } };
type Delivery = { id?: string; name: string; state?: string; result?: string; last_http?: number; created_at?: string };
type Secret = { secret: string; previous_valid_until?: string | null; forId: string };

const codes = {
  invalid_events: "Choose at least one notification event.",
  too_many_notifications: "This site already has as many notifications as it can hold.",
  notification_access_ended: "Notifications for this site are no longer available.",
  notifications_not_enabled: "Notifications are unavailable on this server.",
  invalid_url: "Enter a full link starting with https://",
};

function Deliveries({ slug, id, days, ev }: { slug: string; id: string; days: number; ev: TabProps["d"]["event"] }) {
  const api = useHack();
  const q = useQuery({ queryKey: ["hook-deliveries", slug, id], queryFn: async () => ((await api.webhooks.deliveries(eventSite(slug), id)) as { deliveries?: Delivery[] }).deliveries || [] });
  if (q.isPending) return <p className="mt-2 text-sm text-ink-soft" role="status">Loading deliveries…</p>;
  if (q.isError) return <FieldMessage tone="bad">{errorText(q.error)}</FieldMessage>;
  if (!q.data.length) return <p className="mt-2 text-sm text-ink-soft">No deliveries in the last {days} days.</p>;
  return (
    <ul className="m-0 mt-2 list-none rounded-field border border-rule px-3 py-1" aria-label="Deliveries">
      {q.data.map((x, i) => (
        <li key={x.id || i} className="flex flex-wrap items-baseline gap-x-3 border-b border-rule py-2 last:border-b-0">
          <span className="min-w-0 flex-1">
            <span className="block text-ink">{eventName(x.name)}</span>
            <span className="block text-sm text-ink-soft">
              {NOTIFY_DELIVERY_STATES[x.state || ""] || x.state}
              {x.result ? " · " + (NOTIFY_RESULTS[x.result] || x.result) : ""}
              {x.last_http ? ` (HTTP ${x.last_http})` : ""}
            </span>
          </span>
          <span className="text-sm text-ink-soft">{dateTime(x.created_at, ev)}</span>
        </li>
      ))}
    </ul>
  );
}

function AddHook({ open, onOpenChange, slug, onAdded }: { open: boolean; onOpenChange: (o: boolean) => void; slug: string; onAdded: (s: Secret | null) => void }) {
  const api = useHack();
  const blank = { url: "", events: [NOTIFY_EVENTS[0][0]], label: "", storage: "", headerName: "", headerValue: "" };
  const [f, setF] = useState(blank);
  const [errs, setErrs] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const close = (o: boolean) => {
    if (!o) {
      setF(blank);
      setErrs({});
    }
    onOpenChange(o);
  };
  const save = async () => {
    const e: Record<string, string> = {};
    if (!/^https:\/\/\S+$/i.test(f.url.trim())) e.url = "Enter a full link starting with https://";
    if (!f.events.length) e.events = "Choose at least one event.";
    if (f.headerValue && !f.headerName.trim()) e.header = "Give the header a name, or leave both empty.";
    setErrs(e);
    if (Object.keys(e).length) return;
    const body: Record<string, unknown> = { url: f.url.trim(), events: f.events, storage: f.storage.trim(), label: f.label.trim() };
    if (f.headerName.trim() || f.headerValue) body.auth_header = { name: f.headerName.trim(), value: f.headerValue };
    setBusy(true);
    try {
      const r = (await api.webhooks.create(eventSite(slug), body)) as { notification?: { id: string }; secret?: string };
      onAdded(r.secret && r.notification ? { secret: r.secret, forId: r.notification.id } : null);
      close(false);
    } catch (err) {
      setErrs({ form: errorText(err, codes) });
    } finally {
      setBusy(false);
    }
  };
  return (
    <Dialog open={open} onOpenChange={close} title="Add notification" description="Simple Hack sends a signed message to this address each time it happens.">
      <form
        noValidate
        onSubmit={(e) => {
          e.preventDefault();
          void save();
        }}
      >
        <Field id="notify-url" label="URL" error={errs.url}>
          <Input id="notify-url" type="url" placeholder="https://hooks.slack.com/services/..." value={f.url} onChange={(e) => setF({ ...f, url: e.target.value })} aria-invalid={!!errs.url} />
        </Field>
        <fieldset className="m-0 mb-3 border-0 p-0">
          <legend className="mb-1 text-sm text-ink-soft">Send a message when</legend>
          {NOTIFY_EVENTS.map(([v, l]) => (
            <Check key={v} checked={f.events.includes(v)} onChange={(on) => setF({ ...f, events: on ? [...f.events, v] : f.events.filter((x) => x !== v) })}>
              {l}
            </Check>
          ))}
          {errs.events && <FieldMessage tone="bad">{errs.events}</FieldMessage>}
        </fieldset>
        <Field id="notify-label" label="Label (optional)">
          <Input id="notify-label" maxLength={100} value={f.label} onChange={(e) => setF({ ...f, label: e.target.value })} />
        </Field>
        <Field id="notify-storage" label="Storage (optional)" hint="Leave empty for all saved data.">
          <Input id="notify-storage" maxLength={100} placeholder="all" value={f.storage} onChange={(e) => setF({ ...f, storage: e.target.value })} />
        </Field>
        <details>
          <summary className="flex min-h-11 cursor-pointer items-center text-sm text-ink-soft">Advanced</summary>
          <Field id="notify-header-name" label="Header name (optional)" error={errs.header}>
            <Input id="notify-header-name" maxLength={64} value={f.headerName} onChange={(e) => setF({ ...f, headerName: e.target.value })} />
          </Field>
          <Field id="notify-header-value" label="Header value">
            <Input id="notify-header-value" type="password" maxLength={1024} value={f.headerValue} onChange={(e) => setF({ ...f, headerValue: e.target.value })} />
          </Field>
        </details>
        {errs.form && <FieldMessage tone="bad">{errs.form}</FieldMessage>}
        <DialogActions>
          <Button onClick={() => close(false)}>Cancel</Button>
          <Button type="submit" variant="primary" disabled={busy}>
            Add notification
          </Button>
        </DialogActions>
      </form>
    </Dialog>
  );
}

export function NotificationsTab({ d }: TabProps) {
  const api = useHack();
  const qc = useQueryClient();
  const toast = useToast();
  const slug = d.event.slug;
  const lock = locks(d).admin;
  const custom = (d.event as { website_mode?: string }).website_mode === "custom";
  const q = useQuery({ queryKey: ["event-hooks", slug], queryFn: () => api.webhooks.list(eventSite(slug)) as Promise<List>, enabled: custom });
  const [adding, setAdding] = useState(false);
  const [secret, setSecret] = useState<Secret | null>(null);
  const [open, setOpen] = useState<string | null>(null);
  const [ask, setAsk] = useState<{ kind: "rotate" | "remove"; id: string } | null>(null);
  const [rowMsg, setRowMsg] = useState<{ id: string; text: string; bad: boolean } | null>(null);
  const reload = () => qc.invalidateQueries({ queryKey: ["event-hooks", slug] });
  const help = (
    <p className="text-sm text-ink-soft">
      Available once you put up your own website on <AppLink to={href("organiser", slug, "event-page") + "&tab=website"}>Event page → Website</AppLink>.
    </p>
  );
  const panel = (body: ReactNode, action?: ReactNode) => (
    <>
      <LockNote reason={lock} />
      <Panel title="Notifications" sub="Get a message when a visitor saves something or uploads a file on your own event website." action={action}>
        {body}
      </Panel>
    </>
  );
  if (!custom) return panel(help);
  if (q.isPending) return <TabLoading />;
  if (q.isError) return panel(<Failed error={q.error} retry={() => q.refetch()} />);
  const list = q.data.notifications || [];
  const lim = q.data.limits || {};
  const canManage = lim.can_manage !== false && !lock;
  const pause = async (n: Hook, paused: boolean, undo = true) => {
    try {
      await api.webhooks.update(eventSite(slug), n.id, { paused });
      await reload();
      if (undo) toast(paused ? "Paused" : "Resumed", { undo: { label: "Undo", run: () => void pause(n, !paused, false) } });
    } catch (e) {
      setRowMsg({ id: n.id, text: errorText(e, codes), bad: true });
    }
  };
  const test = async (n: Hook) => {
    try {
      const r = (await api.webhooks.test(eventSite(slug), n.id)) as { delivered?: boolean; result?: string; status?: number };
      setRowMsg({ id: n.id, text: testLine(r), bad: !r.delivered });
    } catch (e) {
      setRowMsg({ id: n.id, text: errorText(e, codes), bad: true });
    }
  };
  const confirm = async () => {
    if (!ask) return;
    try {
      if (ask.kind === "rotate") {
        const r = (await api.webhooks.rotateSecret(eventSite(slug), ask.id)) as { secret: string; previous_valid_until?: string };
        setSecret({ secret: r.secret, previous_valid_until: r.previous_valid_until, forId: ask.id });
      } else {
        await api.webhooks.remove(eventSite(slug), ask.id);
        if (open === ask.id) setOpen(null);
        if (secret?.forId === ask.id) setSecret(null);
        await reload();
        toast("Notification removed.");
      }
    } catch (e) {
      toast(errorText(e, codes), { bad: true });
    }
  };
  const add = lim.can_create && !lock ? <Button onClick={() => setAdding(true)}>Add notification</Button> : undefined;
  return (
    <>
      {panel(
        <>
          {!list.length && (lim.can_create ? <EmptyState title="No notifications set up">Add one to hear when visitors save something or upload a file.</EmptyState> : help)}
          <ul className="m-0 list-none p-0" aria-label="Notifications">
            {list.map((n) => {
              const paused = n.state === "paused" || n.state === "suspended";
              const owner = n.source !== "mcp";
              return (
                <li key={n.id} className="border-b border-rule py-3 last:border-b-0">
                  <div className="flex flex-wrap items-baseline gap-2">
                    <span className="text-ink">{receiverName(n)}</span>
                    <Badge tone={n.state === "active" ? "ok" : n.state === "suspended" || n.state === "expired" ? "down" : "neutral"}>{NOTIFY_STATES[n.state || ""] || n.state}</Badge>
                  </div>
                  <p className="text-sm text-ink-soft">
                    {(n.events || []).map(eventName).join(", ")} · {n.storage || "all saved data"}
                  </p>
                  <p className="text-sm text-ink-soft">{n.last_attempt_at ? (NOTIFY_RESULTS[n.last_result || ""] || "No result yet") + " · " + dateTime(n.last_attempt_at, d.event) : "No deliveries yet"}</p>
                  <div className="mt-2 flex flex-wrap gap-2">
                    <Button onClick={() => setOpen(open === n.id ? null : n.id)} aria-expanded={open === n.id}>
                      {open === n.id ? "Hide deliveries" : "Deliveries"}
                    </Button>
                    {canManage && owner && n.state !== "expired" && <Button onClick={() => void test(n)}>Send test</Button>}
                    {canManage && <Button onClick={() => void pause(n, !paused)}>{paused ? "Resume" : "Pause"}</Button>}
                    {canManage && owner && <Button onClick={() => setAsk({ kind: "rotate", id: n.id })}>Rotate secret</Button>}
                    <Button variant="danger" disabled={!!lock} onClick={() => setAsk({ kind: "remove", id: n.id })}>
                      Remove
                    </Button>
                  </div>
                  {rowMsg?.id === n.id && <FieldMessage tone={rowMsg.bad ? "bad" : "good"}>{rowMsg.text}</FieldMessage>}
                  {open === n.id && <Deliveries slug={slug} id={n.id} days={lim.history_days || 7} ev={d.event} />}
                  {secret?.forId === n.id && (
                    <Notice title="Signing secret. Copy it now. It will not be shown again.">
                      <div className="mt-2 flex flex-wrap gap-2">
                        <Input readOnly aria-label="Signing secret" value={secret.secret} className="min-w-0 flex-[1_1_14rem] font-mono" />
                        <CopyButton text={secret.secret} />
                        <Button onClick={() => setSecret(null)}>Dismiss</Button>
                      </div>
                      {secret.previous_valid_until && <p className="mt-1.5">The previous secret keeps working until {dateTime(secret.previous_valid_until, d.event)}.</p>}
                    </Notice>
                  )}
                </li>
              );
            })}
          </ul>
          {list.length > 0 && !lim.can_create && !lock && <div className="mt-2">{help}</div>}
        </>,
        add,
      )}
      <AddHook
        open={adding}
        onOpenChange={setAdding}
        slug={slug}
        onAdded={(s) => {
          setSecret(s);
          void reload();
          toast(s ? "Notification added. Copy the signing secret below before it disappears." : "Notification added.");
        }}
      />
      <AskDialog
        open={ask?.kind === "rotate"}
        onOpenChange={(o) => !o && setAsk(null)}
        title="Rotate the signing secret?"
        body="The previous secret keeps working for a short overlap window, then stops. Update your receiver with the new one once you have copied it."
        confirmLabel="Rotate"
        onConfirm={confirm}
      />
      <AskDialog open={ask?.kind === "remove"} onOpenChange={(o) => !o && setAsk(null)} title="Remove this notification?" body="It stops firing right away." confirmLabel="Remove" danger onConfirm={confirm} />
    </>
  );
}
