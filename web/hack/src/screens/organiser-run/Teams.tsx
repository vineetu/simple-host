// Teams (/e/{slug}/manage?s=teams&tab=teams|solo): every team, each row
// opening Team detail, with checkboxes to message up to 25 at once; and the
// approved people not on a team, with Fill teams (a preview for a chosen
// "people per team", recomputed as it changes, then Place) and a row menu to
// put one person on a team. CSV exports live on Results.
import { useEffect, useRef, useState } from "react";
import { useSearchParams } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { Badge } from "@kit/components/ui/badge";
import { Button } from "@kit/components/ui/button";
import { DataTable, type ColumnDef } from "@kit/components/ui/data-table";
import { Dialog, DialogActions } from "@kit/components/ui/dialog";
import { EmptyState } from "@kit/components/ui/empty-state";
import { Field, Input, NativeSelect } from "@kit/components/ui/input";
import { Menu } from "@kit/components/ui/menu";
import { Notice } from "@kit/components/ui/notice";
import { PageTitle, Section } from "@kit/components/ui/page-shell";
import { Skeleton } from "@kit/components/ui/skeleton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@kit/components/ui/tabs";
import { useToast } from "@kit/components/ui/toast";
import { useHack } from "@/lib/client";
import { useEventDetail } from "@/app/data";
import { AppLink } from "@/app/nav";
import { Failed } from "@/app/states";
import { href } from "@/routes/resolve";
import type { ScreenProps } from "@/routes/registry";
import { errText, LockNote, RunFrame, useRefreshEvent, writeLock } from "./common";
import {
  adminsLine,
  fillLabel,
  fillSize,
  MAX_SELECTED,
  personName,
  planSignature,
  roomFor,
  teamsSubtitle,
  type FillAnswer,
  type OpenTeams,
  type PlanRow,
  type Solo,
  type Team,
  type TeamList,
} from "./data-teams";
import { TeamMessageDialog } from "./TeamMessage";

const TABS = ["teams", "solo"] as const;
type Tab = (typeof TABS)[number];

/** Pick is a row's checkbox: the whole 44 px square is the target; the box is drawn. */
function Pick({ checked, onChange, label }: { checked: boolean; onChange: (on: boolean) => void; label: string }) {
  return (
    <label className="relative inline-grid size-11 flex-none cursor-pointer place-items-center">
      <input type="checkbox" aria-label={label} checked={checked} onChange={(e) => onChange(e.target.checked)} className="peer absolute inset-0 size-full cursor-pointer appearance-none rounded-field" />
      <span aria-hidden="true" className="grid size-5 place-items-center rounded-sm border-[1.5px] border-ink-soft bg-card text-on-accent peer-checked:border-accent peer-checked:bg-accent">
        {checked && (
          <svg viewBox="0 0 16 16" className="size-3.5" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round">
            <path d="M3.5 8.5l3 3 6-7" />
          </svg>
        )}
      </span>
    </label>
  );
}

function FillDialog({
  slug,
  max,
  initial,
  open,
  onOpenChange,
  onApply,
}: {
  slug: string;
  max: number;
  initial: FillAnswer;
  open: boolean;
  onOpenChange: (o: boolean) => void;
  onApply: (size: number, plan: PlanRow[]) => Promise<void>;
}) {
  const api = useHack();
  const [typed, setTyped] = useState(String(initial.size || Math.min(4, max)));
  const [shown, setShown] = useState<{ size: number; plan: PlanRow[]; skipped: { name: string }[] }>({
    size: initial.size || Math.min(4, max),
    plan: initial.plan || [],
    skipped: initial.skipped || [],
  });
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const token = useRef(0);
  const pending = useRef<Promise<void> | null>(null);
  const timer = useRef(0);

  // Previews the typed size unless it is the one shown (a newer request always wins).
  const preview = (value = typed) => {
    clearTimeout(timer.current);
    const n = fillSize(value, max);
    if (!n || n === shown.size) return null;
    const t = ++token.current;
    const run = api.teams.fill(slug, { apply: false, size: n }).then(
      (r) => {
        if (t !== token.current) return;
        const a = r as FillAnswer;
        setShown({ size: n, plan: a.plan || [], skipped: a.skipped || [] });
      },
      () => {},
    );
    pending.current = run.finally(() => {
      if (t === token.current) pending.current = null;
    });
    return pending.current;
  };
  useEffect(() => () => clearTimeout(timer.current), []);

  const sizeErr = typed.trim() && !fillSize(typed, max) ? `Choose a whole number from 1 to ${max}.` : "";
  return (
    <Dialog open={open} onOpenChange={onOpenChange} title="Fill teams?">
      <form
        noValidate
        onSubmit={async (e) => {
          e.preventDefault();
          if (!fillSize(typed, max)) return;
          // A size typed but not previewed yet: show its plan first, never place blind.
          const p = preview() || pending.current;
          if (p) {
            await p;
            return;
          }
          if (!shown.plan.length) return;
          setBusy(true);
          setErr("");
          try {
            await onApply(shown.size, shown.plan);
            onOpenChange(false);
          } catch (x) {
            setErr(errText(x));
          } finally {
            setBusy(false);
          }
        }}
      >
        <Field id="fill-size" label="People per team" hint={`Up to ${max}, your team size.`} error={sizeErr}>
          <Input
            id="fill-size"
            type="number"
            inputMode="numeric"
            min={1}
            max={max}
            value={typed}
            aria-invalid={!!sizeErr || undefined}
            aria-describedby="fill-size-msg"
            onChange={(e) => {
              setTyped(e.target.value);
              clearTimeout(timer.current);
              const v = e.target.value;
              timer.current = window.setTimeout(() => preview(v), 300);
            }}
            onBlur={() => preview()}
          />
        </Field>
        {shown.plan.length ? (
          <ul className="m-0 list-none p-0" aria-label="The plan">
            {shown.plan.map((p) => (
              <li key={p.team} className="flex gap-3 border-b border-rule py-2 last:border-b-0">
                <span aria-hidden="true" className="text-good">
                  ✓
                </span>
                <span className="min-w-0">
                  <span className="block font-medium">{p.new ? "New team: " + p.name : p.name + " gets"}</span>
                  <span className="block text-sm text-ink-soft">{p.members.map((m) => m.display_name || "Someone").join(", ")}</span>
                </span>
              </li>
            ))}
          </ul>
        ) : (
          <p className="text-ink-soft">Nobody is left to place.</p>
        )}
        {shown.skipped.length > 0 && <p className="mt-2 text-sm text-ink-soft">{shown.skipped.map((s) => `${s.name} is locked, so it was skipped.`).join(" ")}</p>}
        {err && <p role="alert" className="mt-2 text-sm text-bad">{err}</p>}
        <DialogActions>
          <Button onClick={() => onOpenChange(false)}>Cancel</Button>
          <Button type="submit" variant="primary" disabled={busy || !shown.plan.length}>
            {fillLabel(shown.plan)}
          </Button>
        </DialogActions>
      </form>
    </Dialog>
  );
}

function PlaceDialog({ person, choices, onClose, onPlace }: { person: Solo | null; choices: Team[]; onClose: () => void; onPlace: (team: string) => Promise<void> }) {
  const [pick, setPick] = useState("");
  const [busy, setBusy] = useState(false);
  const name = person ? personName(person) : "";
  const value = pick || choices[0]?.slug || "";
  return (
    <Dialog
      open={!!person}
      onOpenChange={(o) => {
        if (!o) {
          setPick("");
          onClose();
        }
      }}
      title={"Put " + name + " on a team"}
    >
      <form
        onSubmit={async (e) => {
          e.preventDefault();
          setBusy(true);
          try {
            await onPlace(value);
            setPick("");
          } finally {
            setBusy(false);
          }
        }}
      >
        <Field id="place-pick" label="Team">
          <NativeSelect id="place-pick" className="w-full" value={value} onChange={(e) => setPick(e.target.value)}>
            {choices.map((t) => (
              <option key={t.slug} value={t.slug}>
                {t.name}
              </option>
            ))}
          </NativeSelect>
        </Field>
        <DialogActions>
          <Button
            onClick={() => {
              setPick("");
              onClose();
            }}
          >
            Cancel
          </Button>
          <Button type="submit" variant="primary" disabled={busy}>
            Put on team
          </Button>
        </DialogActions>
      </form>
    </Dialog>
  );
}

export default function Teams({ match }: ScreenProps) {
  const slug = match.slug!;
  const api = useHack();
  const toast = useToast();
  const refresh = useRefreshEvent(slug);
  const d = useEventDetail(slug).data!;
  const ev = d.event;
  const [params, setParams] = useSearchParams();
  const tabParam = params.get("tab") as Tab;
  const tab: Tab = TABS.includes(tabParam) ? tabParam : "teams";
  const lock = writeLock(d) && (d.admin_view ? "Viewing as platform admin. Everything here is read only." : ev.stage === "archived" ? "This event has ended. Teams can't change any more." : writeLock(d));
  const finding = !!ev.team_finding;
  const q = useQuery({ queryKey: ["org", slug, "teams"], queryFn: () => api.teams.list(slug) as Promise<TeamList> });
  const openQ = useQuery({ queryKey: ["org", slug, "teams-open"], queryFn: () => api.teams.open(slug) as Promise<OpenTeams>, enabled: finding });
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [messaging, setMessaging] = useState(false);
  const [fill, setFill] = useState<FillAnswer | null>(null);
  const [planning, setPlanning] = useState(false);
  const [placing, setPlacing] = useState<Solo | null>(null);

  useEffect(() => {
    if (params.get("tab") !== tab)
      setParams(
        (p) => {
          p.set("tab", tab);
          return p;
        },
        { replace: true },
      );
  }, [params, setParams, tab]);

  const head = (sub?: string) => <PageTitle sub={sub}>Teams</PageTitle>;
  if (q.isPending)
    return (
      <RunFrame slug={slug} screen={match.id}>
        {head()}
        <div className="mt-7" role="status" aria-busy="true" aria-label="Loading teams">
          <Skeleton className="h-11 w-64" />
          <Skeleton className="mt-4 h-16 w-full" />
          <Skeleton className="mt-2 h-16 w-full" />
        </div>
      </RunFrame>
    );
  if (q.isError)
    return (
      <RunFrame slug={slug} screen={match.id}>
        {head()}
        <Failed error={q.error} retry={() => q.refetch()} />
      </RunFrame>
    );

  const teams = q.data.teams || [];
  const max = q.data.team_size_max || Number(ev.team_size_max) || 1;
  const solo = q.data.on_no_team || [];
  const looking = new Map((openQ.data?.solo || []).map((p) => [p.user_id, p.intro || ""]));
  const canMessage = teams.length > 0 && ev.stage !== "archived";
  const fillable = ["open", "building"].includes(ev.stage);
  const reload = () => Promise.all([q.refetch(), openQ.refetch(), refresh()]);

  const choose = (id: string, on: boolean) => {
    const next = new Set(selected);
    if (on) {
      if (next.size >= MAX_SELECTED) {
        toast("You can message up to 25 teams at a time.", { bad: true });
        return;
      }
      next.add(id);
    } else next.delete(id);
    setSelected(next);
  };
  const chosen = teams.filter((t) => selected.has(t.id));
  const msgTitle = "Message " + (chosen.length === 1 ? chosen[0].name : chosen.length + " teams");

  const startFill = async () => {
    setPlanning(true);
    try {
      // No size on the first preview: the server picks min(4, team size) itself.
      setFill((await api.teams.fill(slug, { apply: false })) as FillAnswer);
    } catch (e) {
      toast(errText(e), { bad: true });
    } finally {
      setPlanning(false);
    }
  };
  const applyFill = async (size: number, plan: PlanRow[]) => {
    const r = (await api.teams.fill(slug, { apply: true, size })) as FillAnswer;
    await reload();
    toast(planSignature(plan) !== planSignature(r.plan) ? "Saved. Team membership changed since the preview, so teams were filled a little differently than shown." : "Saved");
  };
  const place = async (team: string) => {
    try {
      await api.teams.addMember(slug, team, { user_id: placing!.user_id });
      setPlacing(null);
      await reload();
      toast("Saved");
    } catch (e) {
      toast(errText(e) === "Something went wrong." ? "Could not move them." : errText(e), { bad: true });
    }
  };

  const teamCols: ColumnDef<Team>[] = [
    ...(canMessage && !lock
      ? [
          {
            id: "pick",
            header: "Select",
            meta: { phone: "end" as const },
            cell: ({ row }: { row: { original: Team } }) => (
              <Pick checked={selected.has(row.original.id)} onChange={(on) => choose(row.original.id, on)} label={"Select " + row.original.name + " to message"} />
            ),
          },
        ]
      : []),
    {
      id: "name",
      header: "Team",
      meta: { phone: "title", wrap: true },
      cell: ({ row }) => (
        <AppLink to={href("organiser", slug, "team") + "&team=" + encodeURIComponent(row.original.slug)} className="inline-flex min-h-11 items-center">
          {row.original.name}
        </AppLink>
      ),
    },
    { id: "track", header: "Track", meta: { phone: "meta" }, cell: ({ row }) => row.original.track?.name || "No track" },
    { id: "admins", header: "Admins", meta: { phone: "meta", wrap: true }, cell: ({ row }) => adminsLine(row.original).replace(/^Admins?: /, "") || "None" },
    {
      id: "size",
      header: "People",
      meta: { phone: "meta" },
      cell: ({ row }) => {
        const t = row.original;
        return (
          <span className="inline-flex flex-wrap items-center gap-1.5">
            {t.members.length} of {max}
            {t.locked ? <Badge tone="down">Locked</Badge> : t.members.length >= max ? <Badge>Full</Badge> : null}
          </span>
        );
      },
    },
  ];
  const soloCols: ColumnDef<Solo>[] = [
    { id: "name", header: "Name", meta: { phone: "title" }, cell: ({ row }) => personName(row.original) },
    {
      id: "looking",
      header: "Status",
      meta: { phone: "meta", wrap: true },
      cell: ({ row }) => {
        const p = row.original;
        return looking.has(p.user_id) ? "Looking for a team" + (looking.get(p.user_id) ? " · " + looking.get(p.user_id) : "") : "Not looking";
      },
    },
    ...(lock
      ? []
      : [
          {
            id: "menu",
            header: "Actions",
            meta: { phone: "end" as const, align: "right" as const },
            cell: ({ row }: { row: { original: Solo } }) => (
              <Menu
                label={"More for " + personName(row.original)}
                items={[
                  {
                    label: "Put on a team…",
                    onSelect: () => (roomFor(teams, max).length ? setPlacing(row.original) : toast("No team has room right now.", { bad: true })),
                  },
                ]}
              />
            ),
          },
        ]),
  ];

  return (
    <RunFrame slug={slug} screen={match.id}>
      {head(teamsSubtitle(teams.length, solo.length))}
      <LockNote reason={lock} />
      <Tabs
        value={tab}
        onValueChange={(v) =>
          setParams((p) => {
            p.set("tab", v);
            return p;
          })
        }
        className="mt-5"
      >
        <TabsList aria-label="Teams sections">
          <TabsTrigger value="teams">Teams ({teams.length})</TabsTrigger>
          <TabsTrigger value="solo">Not on a team ({solo.length})</TabsTrigger>
        </TabsList>
        <TabsContent value="teams">
          <Section
            title="Teams"
            note="Open a team to rename it, place people, choose its admins or lock it."
          >
            {!finding && <Notice title="Team finding is off. People join teams with a link or code." />}
            {canMessage && !lock && (
              <Button className="mb-2" disabled={chosen.length < 1} onClick={() => setMessaging(true)}>
                {chosen.length > 1 ? `Message selected (${chosen.length})` : "Message selected"}
              </Button>
            )}
            <DataTable
              label="Teams"
              data={teams}
              columns={teamCols}
              getRowId={(t) => t.id}
              sortable={false}
              paged={false}
              empty={<EmptyState title="No teams yet">Teams show here once participants start them, or when Fill teams places people.</EmptyState>}
            />
            {teams.length > 0 && (
              <p className="text-sm text-ink-soft">
                Teams hold up to {max} people. Change that in <AppLink to={href("organiser", slug, "settings") + "&tab=teams"}>Settings</AppLink>.{canMessage && !lock ? " Select teams to message them." : ""}
              </p>
            )}
          </Section>
        </TabsContent>
        <TabsContent value="solo">
          <Section title="Not on a team" note="Approved participants without a team. Fill teams places everyone, or use a person’s menu to place one.">
            {fillable && !lock && (
              <Button variant="primary" className="mb-2" disabled={!solo.length || planning} onClick={startFill}>
                Fill teams
              </Button>
            )}
            <DataTable
              label="Not on a team"
              data={solo}
              columns={soloCols}
              getRowId={(p) => p.user_id}
              sortable={false}
              paged={false}
              empty={<EmptyState title="Everyone is on a team">People who join later, or leave a team, show here.</EmptyState>}
            />
          </Section>
        </TabsContent>
      </Tabs>
      <p className="text-sm text-ink-soft">
        Team and participant lists download as CSV from <AppLink to={href("organiser", slug, "results")}>Results</AppLink>.
      </p>
      <TeamMessageDialog
        slug={slug}
        open={messaging}
        onOpenChange={setMessaging}
        teamIds={chosen.map((t) => t.id)}
        title={msgTitle}
        onSent={(n) => {
          setSelected(new Set());
          toast("Sent to " + n + (n === 1 ? " person" : " people"));
          void reload();
        }}
      />
      {fill && <FillDialog slug={slug} max={max} initial={fill} open onOpenChange={(o) => !o && setFill(null)} onApply={applyFill} />}
      <PlaceDialog person={placing} choices={roomFor(teams, max)} onClose={() => setPlacing(null)} onPlace={place} />
    </RunFrame>
  );
}
