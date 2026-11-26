// People (/e/{slug}/manage?s=people): applications waiting on the organiser
// (with filters once anything waits), then everyone in the event with a role
// filter and Remove. Approve is one press; Reject and Remove ask first, since
// they reach the person and cannot be undone here.
import { useState, type ReactNode } from "react";
import { useLocation } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Badge } from "@kit/components/ui/badge";
import { Button } from "@kit/components/ui/button";
import { AskDialog } from "@kit/components/ui/dialog";
import { Label, NativeSelect } from "@kit/components/ui/input";
import { Menu } from "@kit/components/ui/menu";
import { PageTitle, Section } from "@kit/components/ui/page-shell";
import { Skeleton } from "@kit/components/ui/skeleton";
import { useToast } from "@kit/components/ui/toast";
import { useHack } from "@/lib/client";
import { stageLabel } from "@/lib/labels";
import { dateTime } from "@/lib/time";
import { useEventDetail } from "@/app/data";
import { AppLink } from "@/app/nav";
import { Failed } from "@/app/states";
import { href } from "@/routes/resolve";
import type { ScreenProps } from "@/routes/registry";
import { errText, LockNote, RunFrame, useRefreshEvent, writeLock } from "./common";
import {
  AGE_RANGE_LABELS,
  countryName,
  filtersActive,
  GENDER_LABELS,
  matchesRole,
  NO_FILTERS,
  personBits,
  rejectBody,
  removeBody,
  ROLE_FILTERS,
  visibleApplications,
  type AppFilters,
  type Application,
  type Person,
  type RoleFilter,
} from "./data-people";

const box = "rounded-wobble border border-rule bg-card px-4 py-4 md:px-5";

function Facts({ a }: { a: Application }) {
  const place = [a.city, a.country ? countryName(a.country) : ""].filter(Boolean).join(", ");
  const bits: ReactNode[] = [];
  if (place) bits.push(place);
  if (a.age_range) bits.push(a.age_range === "under_18" ? <Badge tone="down">Under 18</Badge> : AGE_RANGE_LABELS[a.age_range] || a.age_range);
  if (a.gender) bits.push(GENDER_LABELS[a.gender] || a.gender);
  if (!bits.length) return <p className="mt-1 text-sm text-ink-soft">Didn’t share location, age range or gender</p>;
  return (
    <p className="mt-1 text-sm">
      {bits.map((b, i) => (
        <span key={i}>
          {i > 0 && " · "}
          {b}
        </span>
      ))}
    </p>
  );
}

function ApplicationCard({ a, tz, locked, onApprove, onReject }: { a: Application; tz?: string; locked: boolean; onApprove: () => Promise<void>; onReject: () => void }) {
  const [busy, setBusy] = useState(false);
  return (
    <li className={box}>
      <h3 className="type-section">{a.display_name}</h3>
      <p className="mt-0.5 text-sm text-ink-soft">
        {a.email ? a.email + " · " : ""}
        <Badge>{a.joined_via === "link" ? "Join link" : "Applied"}</Badge>
      </p>
      <p className="mt-1 text-sm text-ink-soft">Applied {dateTime(a.joined_at, tz)}</p>
      <Facts a={a} />
      {(a.answers || []).length > 0 && (
        <dl className="mt-3">
          {(a.answers || []).map((q, i) => (
            <div key={i} className="mt-2">
              <dt className="text-sm text-ink-soft">{q.prompt}</dt>
              <dd className={q.answer ? "m-0" : "m-0 text-ink-faint italic"}>{q.answer || "No answer"}</dd>
            </div>
          ))}
        </dl>
      )}
      <div className="mt-4 flex flex-wrap gap-2.5">
        <Button
          disabled={locked || busy}
          onClick={async () => {
            setBusy(true);
            await onApprove();
            setBusy(false);
          }}
        >
          Approve
        </Button>
        <Button variant="danger" disabled={locked || busy} onClick={onReject}>
          Reject
        </Button>
      </div>
    </li>
  );
}

function Filters({ pending, f, set }: { pending: Application[]; f: AppFilters; set: (f: AppFilters) => void }) {
  const applied = pending.filter((a) => a.joined_via !== "link").length;
  const link = pending.length - applied;
  const countries = [...new Set(pending.map((a) => a.country).filter(Boolean) as string[])]
    .map((code) => ({ code, name: countryName(code) || code }))
    .sort((a, b) => a.name.localeCompare(b.name));
  const sel = (id: string, label: string, value: string, onChange: (v: string) => void, options: [string, string][]) => (
    <div className="min-w-0">
      <Label htmlFor={id}>{label}</Label>
      <NativeSelect id={id} className="w-full" value={value} onChange={(e) => onChange(e.target.value)}>
        {options.map(([v, l]) => (
          <option key={v} value={v}>
            {l}
          </option>
        ))}
      </NativeSelect>
    </div>
  );
  return (
    <div className="mb-4 grid grid-cols-2 gap-3 md:grid-cols-4">
      {sel("app-filter-from", "From", f.from, (v) => set({ ...f, from: v as AppFilters["from"] }), [
        ["all", `Everyone waiting (${pending.length})`],
        ["applied", `Applied (${applied})`],
        ["link", `Join link (${link})`],
      ])}
      {sel("app-filter-age", "Age range", f.age, (v) => set({ ...f, age: v }), [["", "Any"], ...Object.entries(AGE_RANGE_LABELS)])}
      {sel("app-filter-country", "Country", f.country, (v) => set({ ...f, country: v }), [["", "Any"], ...countries.map((c) => [c.code, c.name] as [string, string])])}
      {sel("app-filter-sort", "Sort", f.sort, (v) => set({ ...f, sort: v as AppFilters["sort"] }), [
        ["oldest", "Oldest first"],
        ["newest", "Newest first"],
      ])}
    </div>
  );
}

function PersonRow({ p, slug, tz, locked, onRemove }: { p: Person; slug: string; tz?: string; locked: boolean; onRemove: () => void }) {
  const when = dateTime(p.joined_at, tz, { dateOnly: true });
  const name = p.display_name || "Someone";
  return (
    <li className="flex min-h-14 items-center gap-3 border-b border-rule px-1 py-2 last:border-b-0">
      <span className="min-w-0 flex-1">
        <span className="block font-semibold text-ink">{name}</span>
        <span className="block text-sm text-ink-soft">{personBits(p, when)}</span>
      </span>
      {p.primary_organiser ? (
        <span className="flex-none text-sm text-ink-soft">Event creator</span>
      ) : p.role === "organiser" ? (
        <AppLink to={href("organiser", slug, "settings") + "&tab=organisers"} className="inline-flex min-h-11 flex-none items-center text-sm">
          Organisers
        </AppLink>
      ) : locked ? null : (
        <Menu label={"More for " + name} items={[{ label: "Remove from event", danger: true, onSelect: onRemove }]} />
      )}
    </li>
  );
}

export default function People({ match }: ScreenProps) {
  const slug = match.slug!;
  const api = useHack();
  const qc = useQueryClient();
  const toast = useToast();
  const refreshEvent = useRefreshEvent(slug);
  const { search, hash } = useLocation();
  const d = useEventDetail(slug).data;
  const ev = d?.event;
  const tz = ev?.time_zone;
  const apps = useQuery({ queryKey: ["applications", slug], queryFn: async () => ((await api.people.applications(slug)) as unknown as Application[]) || [] });
  const people = useQuery({ queryKey: ["people", slug], queryFn: async () => ((await api.people.list(slug)) as unknown as Person[]) || [] });
  const [role, setRole] = useState<RoleFilter>(new URLSearchParams(search).get("role") === "judge" || hash === "#judges" ? "Judges" : "Everyone");
  const [f, setF] = useState<AppFilters>(NO_FILTERS);
  const [reject, setReject] = useState<Application | null>(null);
  const [remove, setRemove] = useState<Person | null>(null);

  const lock = d ? (d.admin_view ? "Viewing as platform admin. Everything here is read only." : ev?.stage === "archived" && !ev.taken_down ? "This event has ended, so you can't approve or remove anyone." : writeLock(d)) : "";
  const reload = () => Promise.all([qc.invalidateQueries({ queryKey: ["applications", slug] }), qc.invalidateQueries({ queryKey: ["people", slug] })]);

  const body = (() => {
    if (apps.isPending || people.isPending)
      return (
        <>
          <PageTitle>People</PageTitle>
          <div className="mt-7" role="status" aria-busy="true" aria-label="Loading people">
            <Skeleton className="h-40 w-full" />
            <Skeleton className="mt-3 h-16 w-full" />
            <Skeleton className="mt-2 h-16 w-full" />
          </div>
        </>
      );
    if (apps.isError || people.isError)
      return (
        <>
          <PageTitle>People</PageTitle>
          <Failed error={apps.error || people.error} retry={() => void reload()} />
        </>
      );
    const pending = (apps.data || []).filter((a) => a.status === "pending");
    const shown = visibleApplications(pending, f);
    const everyone = (people.data || []).filter((p) => matchesRole(p, role));
    const decide = async (a: Application, decision: "approved" | "rejected") => {
      try {
        await api.people.decide(slug, a.user_id, { decision });
      } catch (e) {
        toast(errText(e), { bad: true });
        return;
      }
      await Promise.all([reload(), refreshEvent()]);
      toast("Application " + decision + ".");
    };
    return (
      <>
        <PageTitle sub={pending.length ? pending.length + " application" + (pending.length === 1 ? "" : "s") + " waiting on you" : "Nobody is waiting for approval"}>People</PageTitle>
        <LockNote reason={lock} />
        {(pending.length > 0 || ev?.stage === "draft") && (
          <Section title="Applications">
            {pending.length ? (
              <>
                <Filters pending={pending} f={f} set={setF} />
                {shown.length ? (
                  <ul className="m-0 grid list-none gap-3 p-0" aria-label="Applications waiting">
                    {shown.map((a) => (
                      <ApplicationCard key={a.user_id} a={a} tz={tz} locked={!!lock} onApprove={() => decide(a, "approved")} onReject={() => setReject(a)} />
                    ))}
                  </ul>
                ) : (
                  <div className="flex flex-wrap items-center gap-3 rounded-field border border-rule bg-card px-4 py-3">
                    <span className="text-ink-soft">Nobody waiting matches these filters.</span>
                    {filtersActive(f) && <Button onClick={() => setF(NO_FILTERS)}>Clear filters</Button>}
                  </div>
                )}
              </>
            ) : (
              <p className="rounded-field border border-rule bg-card px-4 py-3 text-ink-soft">
                Sign-up isn't open yet. Move the event to {stageLabel("open")} to start taking applications.
              </p>
            )}
          </Section>
        )}
        <Section title="Everyone">
          <div id="judges" className="mb-2 flex items-center gap-2 md:max-w-card">
            <label htmlFor="role-filter" className="type-label text-ink-soft">
              Show
            </label>
            <NativeSelect id="role-filter" className="flex-1" value={role} onChange={(e) => setRole(e.target.value as RoleFilter)}>
              {ROLE_FILTERS.map((r) => (
                <option key={r}>{r}</option>
              ))}
            </NativeSelect>
          </div>
          {everyone.length ? (
            <ul aria-label="Everyone in the event" className="m-0 list-none rounded-wobble border border-rule bg-card px-3 py-1">
              {everyone.map((p) => (
                <PersonRow key={p.user_id} p={p} slug={slug} tz={tz} locked={!!lock} onRemove={() => setRemove(p)} />
              ))}
            </ul>
          ) : (
            <p className="text-ink-soft">Nobody matches that filter.</p>
          )}
        </Section>
        <p className="mt-6 flex flex-wrap gap-x-4">
          <AppLink to={href("organiser", slug, "settings") + "&tab=signup"} className="inline-flex min-h-11 items-center">
            Sign-up settings
          </AppLink>
          <AppLink to={href("organiser", slug, "settings") + "&tab=organisers"} className="inline-flex min-h-11 items-center">
            Organisers
          </AppLink>
        </p>
      </>
    );
  })();

  return (
    <RunFrame slug={slug} screen={match.id}>
      {body}
      <AskDialog
        open={!!reject}
        onOpenChange={(o) => !o && setReject(null)}
        title="Reject this application?"
        body={reject ? rejectBody(reject.display_name) : ""}
        confirmLabel="Reject"
        danger
        onConfirm={async () => {
          const a = reject!;
          try {
            await api.people.decide(slug, a.user_id, { decision: "rejected" });
          } catch (e) {
            toast(errText(e), { bad: true });
            return;
          }
          await Promise.all([reload(), refreshEvent()]);
          toast("Application rejected.");
        }}
      />
      <AskDialog
        open={!!remove}
        onOpenChange={(o) => !o && setRemove(null)}
        title={"Remove " + (remove?.display_name || "this person") + " from this event?"}
        body={remove ? removeBody(remove.display_name || "this person", remove.role) : ""}
        confirmLabel="Remove"
        danger
        onConfirm={async () => {
          try {
            await api.people.remove(slug, remove!.user_id);
          } catch (e) {
            toast(errText(e), { bad: true });
            return;
          }
          await Promise.all([qc.invalidateQueries({ queryKey: ["people", slug] }), refreshEvent()]);
          toast("Removed");
        }}
      />
    </RunFrame>
  );
}
