// Settings → Organiser details, Organisers (co-organiser invitations and
// removal) and Links (the join and judge links).
import { useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { AskDialog } from "@kit/components/ui/dialog";
import { Button } from "@kit/components/ui/button";
import { CopyButton } from "@kit/components/ui/copy";
import { EmptyState } from "@kit/components/ui/empty-state";
import { Field, Input, Textarea } from "@kit/components/ui/input";
import { Badge } from "@kit/components/ui/badge";
import { useToast } from "@kit/components/ui/toast";
import { useHack } from "@/lib/client";
import { dateTime } from "@/lib/time";
import { useMe } from "@/app/data";
import { Failed } from "@/app/states";
import type { TabProps } from "./Settings";
import { errorText, LockNote, Panel, SaveFoot, useRefreshEvent, useUnsavedGuard } from "./common";
import { locks, TabLoading, useAnchor } from "./settings-ui";

type Org = {
  organiser_name?: string;
  organisation?: string;
  contact_email?: string;
  purpose?: string;
  expected_participants?: number | null;
  join_url?: string;
  judge_url?: string;
};
const org = (d: TabProps["d"]) => (d.organiser || {}) as Org;

export function DetailsTab({ d }: TabProps) {
  const api = useHack();
  const toast = useToast();
  const slug = d.event.slug;
  const refresh = useRefreshEvent(slug);
  // The service keeps these editable after the event ends; only an inspection stops them.
  const lock = locks(d).admin;
  const o = org(d);
  const saved = { name: o.organiser_name || "", org: o.organisation || "", email: o.contact_email || "", purpose: o.purpose || "", n: o.expected_participants == null ? "" : String(o.expected_participants) };
  const key = JSON.stringify(saved);
  const [f, setF] = useState(saved);
  const [errs, setErrs] = useState<Record<string, string>>({});
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  // eslint-disable-next-line react-hooks/exhaustive-deps
  useEffect(() => setF(saved), [key]);
  const dirty = JSON.stringify(f) !== key;
  useUnsavedGuard(dirty);
  const save = async () => {
    const e: Record<string, string> = {};
    if (!/^[1-9]\d*$/.test(f.n.trim()) || Number(f.n) > 100000) e.n = "Expected participants must be between 1 and 100000.";
    if (f.email.trim() && !/^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(f.email.trim())) e.email = "Enter an email address like name@example.com.";
    setErrs(e);
    if (Object.keys(e).length) return;
    setBusy(true);
    setError("");
    try {
      await api.event.update(slug, { organiser_name: f.name, organisation: f.org, contact_email: f.email, purpose: f.purpose, expected_participants: Number(f.n) });
      await refresh();
      toast("Saved");
    } catch (err) {
      setError(errorText(err));
    } finally {
      setBusy(false);
    }
  };
  return (
    <>
      <LockNote text={lock} />
      <Panel title="Organiser details">
        <form
          noValidate
          onSubmit={(e) => {
            e.preventDefault();
            void save();
          }}
        >
          <Field id="set-name" label="Your name" hint="Shown on the event page.">
            <Input id="set-name" maxLength={100} value={f.name} disabled={!!lock} onChange={(e) => setF({ ...f, name: e.target.value })} />
          </Field>
          <Field id="set-org" label="Organisation or community">
            <Input id="set-org" maxLength={120} value={f.org} disabled={!!lock} onChange={(e) => setF({ ...f, org: e.target.value })} />
          </Field>
          <Field id="set-email" label="Contact email" error={errs.email}>
            <Input id="set-email" type="email" value={f.email} disabled={!!lock} onChange={(e) => setF({ ...f, email: e.target.value })} aria-invalid={!!errs.email} />
          </Field>
          <Field id="set-purpose" label="What the event is for" hint="Only you and Simple Hack see this.">
            <Textarea id="set-purpose" maxLength={1000} value={f.purpose} disabled={!!lock} onChange={(e) => setF({ ...f, purpose: e.target.value })} />
          </Field>
          <Field id="set-n" label="Expected participants" error={errs.n}>
            <Input id="set-n" type="number" inputMode="numeric" min={1} max={100000} value={f.n} disabled={!!lock} onChange={(e) => setF({ ...f, n: e.target.value })} className="max-w-40" aria-invalid={!!errs.n} />
          </Field>
          <SaveFoot dirty={dirty} busy={busy} disabled={!!lock} error={error} onDiscard={() => setF(saved)} />
        </form>
      </Panel>
    </>
  );
}

type Person = { user_id: string; display_name: string; role?: string; primary_organiser?: boolean; email?: string };
type Ask = "create" | "revoke" | { remove: Person } | null;

export function OrganisersTab({ d }: TabProps) {
  const api = useHack();
  const qc = useQueryClient();
  const toast = useToast();
  const slug = d.event.slug;
  const lock = locks(d).settings;
  const me = useMe().data;
  const people = useQuery({ queryKey: ["people", slug], queryFn: async () => ((await api.people.list(slug)) as unknown as Person[]) || [] });
  const [invite, setInvite] = useState<{ url: string; expires_at?: string } | null>(null);
  const [ask, setAsk] = useState<Ask>(null);
  useAnchor(!people.isPending);
  if (people.isPending) return <TabLoading />;
  if (people.isError) return <Failed error={people.error} retry={() => people.refetch()} />;
  const organisers = people.data.filter((p) => p.role === "organiser");
  const label = (p: Person) => {
    const you = !!me?.id && me.id === p.user_id;
    return p.display_name + (p.primary_organiser ? (you ? " (you, creator)" : " (creator)") : you ? " (you)" : "");
  };
  const fail = (e: unknown) => {
    toast(errorText(e), { bad: true });
    throw e;
  };
  const confirm = async () => {
    if (ask === "create") {
      const r = (await api.people.createOrganiserInvite(slug).catch(fail)) as { url: string; expires_at?: string };
      setInvite({ url: r.url, expires_at: r.expires_at });
    } else if (ask === "revoke") {
      await api.people.revokeOrganiserInvite(slug).catch(fail);
      setInvite(null);
      toast("Invitation revoked.");
    } else if (ask && "remove" in ask) {
      await api.people.removeOrganiser(slug, ask.remove.user_id).catch(fail);
      setInvite(null);
      await qc.invalidateQueries({ queryKey: ["people", slug] });
      toast(ask.remove.display_name + " is no longer an organiser.");
    }
  };
  return (
    <>
      <LockNote text={lock} />
      <Panel id="coorg" title="Organisers" sub="A co-organiser can do everything you can on this event, including Delete this event. Only the creator can’t be removed.">
        <ul className="m-0 list-none rounded-field border border-rule px-3 py-1" aria-label="Organisers">
          {organisers.map((p) => (
            <li key={p.user_id} className="flex min-h-14 flex-wrap items-center gap-3 border-b border-rule py-2 last:border-b-0">
              <span className="min-w-0 flex-1">
                <span className="block text-ink">{label(p)}</span>
                {p.email && <span className="block text-sm text-ink-soft">{p.email}</span>}
              </span>
              {p.primary_organiser ? (
                <Badge>Event creator</Badge>
              ) : (
                <Button variant="danger" disabled={!!lock} onClick={() => setAsk({ remove: p })}>
                  Remove co-organiser
                </Button>
              )}
            </li>
          ))}
        </ul>
        {organisers.length <= 1 && <EmptyState title="No co-organisers yet">Create an invitation below and send it to someone you trust to run this event with you.</EmptyState>}
      </Panel>
      <Panel title="Invite a co-organiser" sub="Anyone with this link can become an organiser until you revoke it or it expires in 7 days. Creating a new link stops the old one.">
        {invite ? (
          <div>
            <label htmlFor="coorg-url" className="mb-1.5 block text-sm text-ink-soft">
              Invitation
            </label>
            <div className="flex flex-wrap gap-2">
              <Input id="coorg-url" readOnly value={invite.url} className="min-w-0 flex-[1_1_16rem] font-mono" />
              <CopyButton text={invite.url} label="Copy invitation" />
            </div>
            <p className="mt-1.5 text-sm text-ink-soft">{invite.expires_at ? "Expires " + dateTime(invite.expires_at, d.event) + ". " : ""}The link shows only here, right after you create it.</p>
          </div>
        ) : (
          <p className="text-sm text-ink-soft">An invitation link shows only once, right after you create it. One you made earlier keeps working until it is used, expires or you revoke it.</p>
        )}
        <div className="mt-3 flex flex-wrap gap-2.5">
          <Button disabled={!!lock} onClick={() => setAsk("create")}>
            Create invitation
          </Button>
          <Button variant="danger" disabled={!!lock} onClick={() => setAsk("revoke")}>
            Revoke invitation
          </Button>
        </div>
      </Panel>
      <AskDialog
        open={ask === "create"}
        onOpenChange={(o) => !o && setAsk(null)}
        title="Create co-organiser invitation?"
        body="Anyone with this link gets full control of this event, including deleting it, until you revoke it or it expires in 7 days."
        confirmLabel="Create invitation"
        onConfirm={confirm}
      />
      <AskDialog
        open={ask === "revoke"}
        onOpenChange={(o) => !o && setAsk(null)}
        title="Revoke this invitation?"
        body="The link stops working for anyone who hasn’t accepted yet. People who already accepted stay organisers."
        confirmLabel="Revoke"
        danger
        onConfirm={confirm}
      />
      <AskDialog
        open={!!ask && typeof ask === "object"}
        onOpenChange={(o) => !o && setAsk(null)}
        title={"Remove " + (ask && typeof ask === "object" ? ask.remove.display_name : "") + " as co-organiser?"}
        body="They lose manage access to this event right away. (The event’s original creator can’t be removed.) This also revokes the current invitation link."
        confirmLabel="Remove co-organiser"
        danger
        onConfirm={confirm}
      />
    </>
  );
}

export function LinksTab({ d }: TabProps) {
  const api = useHack();
  const toast = useToast();
  const slug = d.event.slug;
  const refresh = useRefreshEvent(slug);
  const lock = locks(d).settings;
  const o = org(d);
  const [ask, setAsk] = useState<"join" | "judge" | null>(null);
  const row = (kind: "join" | "judge", title: string, url = "") => (
    <Panel title={title}>
      <div className="flex flex-wrap gap-2">
        <Input id={kind + "-url"} aria-label={title} readOnly value={url} className="min-w-0 flex-[1_1_16rem] font-mono" />
        <CopyButton text={url} />
        <Button variant="danger" disabled={!!lock} onClick={() => setAsk(kind)}>
          New link
        </Button>
      </div>
    </Panel>
  );
  return (
    <>
      <LockNote text={lock} />
      {row("join", "Join link", o.join_url)}
      {row("judge", "Judge link", o.judge_url)}
      <AskDialog
        open={!!ask}
        onOpenChange={(open) => !open && setAsk(null)}
        title="Make a new link?"
        body={ask === "join" ? "The old link stops working, even for people who have not used it yet. People already in the event stay." : "The old judge link stops working. Judges already in stay."}
        confirmLabel="New link"
        danger
        onConfirm={async () => {
          try {
            await api.event.newCode(slug, ask!);
            await refresh();
            toast(ask === "join" ? "New join link made. The old one no longer works." : "New judge link made. The old one no longer works.");
          } catch (e) {
            toast(errorText(e), { bad: true });
          }
        }}
      />
    </>
  );
}
