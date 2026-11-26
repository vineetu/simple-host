// Team detail (/e/{slug}/manage?s=team&team=…): one team as the organiser
// runs it. Members (add someone not on a team, move to another team, make or
// remove admin, take off), rename, track (when it matters), the messages
// thread with Message team, joining (lock, the team link, a new link), the
// entry and site at a glance, and Delete team. Lock, admin and move write
// straight away with Undo; taking someone off, a new link and deleting ask first.
import { useState, type ReactNode } from "react";
import { useSearchParams } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { Badge } from "@kit/components/ui/badge";
import { Button } from "@kit/components/ui/button";
import { CopyButton } from "@kit/components/ui/copy";
import { AskDialog, Dialog, DialogActions } from "@kit/components/ui/dialog";
import { EmptyState } from "@kit/components/ui/empty-state";
import { Check } from "@kit/components/ui/choice";
import { Field, Input, NativeSelect } from "@kit/components/ui/input";
import { Menu } from "@kit/components/ui/menu";
import { Notice } from "@kit/components/ui/notice";
import { PageTitle, Section } from "@kit/components/ui/page-shell";
import { Skeleton } from "@kit/components/ui/skeleton";
import { useToast } from "@kit/components/ui/toast";
import { useHack } from "@/lib/client";
import { useEventDetail } from "@/app/data";
import { AppLink, useGo } from "@/app/nav";
import { Failed } from "@/app/states";
import { href } from "@/routes/resolve";
import type { ScreenProps } from "@/routes/registry";
import { errText, LockNote, RunFrame, useRefreshEvent, writeLock } from "./common";
import { entryPill, joinAnd, linkText, personName, roomFor, sitePill, teamLinkUrl, type Entries, type Member, type Message, type Solo, type Team, type TeamList } from "./data-teams";
import { MessageRows, TeamMessageDialog } from "./TeamMessage";

type Track = { slug: string; name: string };
type People = { user_id: string; email?: string }[];

function Card({ title, children }: { title: string; children: ReactNode }) {
  return (
    <Section title={title} className="mt-0 rounded-wobble border border-rule bg-card px-4 py-4 first:mt-0 md:px-5">
      {children}
    </Section>
  );
}

/** PickDialog is one choice from a list (a person, a team), then a button. */
function PickDialog({
  open,
  title,
  label,
  options,
  confirm,
  onClose,
  onPick,
}: {
  open: boolean;
  title: string;
  label: string;
  options: { value: string; label: string }[];
  confirm: string;
  onClose: () => void;
  onPick: (v: string) => Promise<void>;
}) {
  const [v, setV] = useState("");
  const [busy, setBusy] = useState(false);
  const value = v || options[0]?.value || "";
  const close = () => {
    setV("");
    onClose();
  };
  return (
    <Dialog open={open} onOpenChange={(o) => !o && close()} title={title}>
      <form
        onSubmit={async (e) => {
          e.preventDefault();
          setBusy(true);
          try {
            await onPick(value);
            setV("");
          } finally {
            setBusy(false);
          }
        }}
      >
        <Field id="pick-one" label={label}>
          <NativeSelect id="pick-one" className="w-full" value={value} onChange={(e) => setV(e.target.value)}>
            {options.map((o) => (
              <option key={o.value} value={o.value}>
                {o.label}
              </option>
            ))}
          </NativeSelect>
        </Field>
        <DialogActions>
          <Button onClick={close}>Cancel</Button>
          <Button type="submit" variant="primary" disabled={busy}>
            {confirm}
          </Button>
        </DialogActions>
      </form>
    </Dialog>
  );
}

function Rename({ team, disabled, onSave }: { team: Team; disabled: boolean; onSave: (name: string) => Promise<string> }) {
  const [name, setName] = useState(team.name);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  return (
    <form
      noValidate
      onSubmit={async (e) => {
        e.preventDefault();
        const n = name.trim();
        if (!n) {
          setErr("Enter a team name.");
          return;
        }
        setBusy(true);
        setErr(await onSave(n));
        setBusy(false);
      }}
    >
      <Field id="rename-name" label="Team name" error={err}>
        <div className="flex flex-wrap gap-2.5">
          <Input
            id="rename-name"
            className="min-w-48 flex-1"
            maxLength={80}
            value={name}
            disabled={disabled}
            aria-invalid={!!err || undefined}
            aria-describedby="rename-name-msg"
            onChange={(e) => {
              setName(e.target.value);
              if (err) setErr("");
            }}
          />
          <Button type="submit" disabled={disabled || busy || name.trim() === team.name}>
            Save
          </Button>
        </div>
      </Field>
    </form>
  );
}

function TrackPick({ team, tracks, disabled, onSave }: { team: Team; tracks: Track[]; disabled: boolean; onSave: (track: string) => Promise<void> }) {
  const [v, setV] = useState(team.track?.slug || "");
  return (
    <form
      onSubmit={async (e) => {
        e.preventDefault();
        await onSave(v);
      }}
    >
      <Field id="team-track" label="Track">
        <div className="flex flex-wrap gap-2.5">
          <NativeSelect id="team-track" className="min-w-48 flex-1" value={v} disabled={disabled} onChange={(e) => setV(e.target.value)}>
            <option value="">No track</option>
            {tracks.map((t) => (
              <option key={t.slug} value={t.slug}>
                {t.name}
              </option>
            ))}
          </NativeSelect>
          <Button type="submit" disabled={disabled || v === (team.track?.slug || "")}>
            Save
          </Button>
        </div>
      </Field>
    </form>
  );
}

export default function TeamDetail({ match }: ScreenProps) {
  const slug = match.slug!;
  const api = useHack();
  const toast = useToast();
  const go = useGo();
  const refresh = useRefreshEvent(slug);
  const d = useEventDetail(slug).data!;
  const ev = d.event;
  const [params] = useSearchParams();
  const teamSlug = params.get("team") || "";
  const tracks = (Array.isArray(ev.tracks) ? ev.tracks : []) as Track[];
  const max = Number(ev.team_size_max) || 1;

  const teamsQ = useQuery({ queryKey: ["org", slug, "teams"], queryFn: () => api.teams.list(slug) as Promise<TeamList> });
  const peopleQ = useQuery({ queryKey: ["org", slug, "people"], queryFn: async () => (await api.people.list(slug)) as unknown as People });
  const entriesQ = useQuery({ queryKey: ["org", slug, "entries"], queryFn: () => api.entries.list(slug) as Promise<Entries> });
  const msgQ = useQuery({ queryKey: ["org", slug, "team-messages", teamSlug], queryFn: () => api.messages.teamThread(slug, teamSlug) as Promise<{ messages?: Message[] }>, enabled: !!teamSlug });
  const judgingQ = useQuery({ queryKey: ["org", slug, "judging-settings"], queryFn: () => api.judging.settings(slug) as Promise<{ assignment_mode?: string }>, enabled: tracks.length > 0 });

  const [adding, setAdding] = useState(false);
  const [moving, setMoving] = useState<Member | null>(null);
  const [removing, setRemoving] = useState<Member | null>(null);
  const [newLink, setNewLink] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [messaging, setMessaging] = useState(false);

  const crumbs = (name?: string) => (
    <p className="mt-6 text-sm text-ink-soft">
      <AppLink to={href("organiser", slug, "teams")} className="inline-flex min-h-11 items-center">
        Teams
      </AppLink>
      {name ? " / " + name : ""}
    </p>
  );
  if (teamsQ.isPending)
    return (
      <RunFrame slug={slug} screen={match.id}>
        {crumbs()}
        <div role="status" aria-busy="true" aria-label="Loading the team">
          <Skeleton className="h-8 w-56" />
          <Skeleton className="mt-6 h-32 w-full" />
          <Skeleton className="mt-3 h-24 w-full" />
        </div>
      </RunFrame>
    );
  if (teamsQ.isError)
    return (
      <RunFrame slug={slug} screen={match.id}>
        {crumbs()}
        <Failed error={teamsQ.error} retry={() => teamsQ.refetch()} />
      </RunFrame>
    );
  const teams = teamsQ.data.teams || [];
  const team = teams.find((t) => t.slug === teamSlug);
  if (!team)
    return (
      <RunFrame slug={slug} screen={match.id}>
        {crumbs()}
        <EmptyState
          title="Team not found"
          action={
            <Button asChild>
              <AppLink to={href("organiser", slug, "teams")}>All teams</AppLink>
            </Button>
          }
        >
          It may have been deleted, or the link is wrong.
        </EmptyState>
      </RunFrame>
    );

  const lock = writeLock(d);
  const solo: Solo[] = teamsQ.data.on_no_team || [];
  const people = Array.isArray(peopleQ.data) ? peopleQ.data : [];
  const members = team.members.map((m) => ({ ...m, email: people.find((p) => p.user_id === m.user_id)?.email || m.email || "" }));
  const full = team.members.length >= max;
  const entry = (entriesQ.data?.entries || []).find((e) => e.team.slug === teamSlug) || null;
  const ep = entryPill(entry, entriesQ.data?.required || []);
  const sp = sitePill(team);
  const entryHref = href("organiser", slug, "entry") + "&team=" + encodeURIComponent(teamSlug);
  const trackLocked = !!lock || !!ev.judging_locked_at || ["judging", "results", "archived"].includes(ev.stage);
  const showTrack = tracks.length > 0 && (judgingQ.data?.assignment_mode === "panel" || !team.track);
  const messages = msgQ.data?.messages || [];
  const link = teamLinkUrl(team, location.origin);
  const reload = () => Promise.all([teamsQ.refetch(), entriesQ.refetch(), refresh()]);

  // A write, then the screen read again; a refusal says why.
  const act = async (run: () => Promise<unknown>, done?: string | ((r: unknown) => void), fallback?: string) => {
    try {
      const r = await run();
      await reload();
      if (typeof done === "function") done(r);
      else toast(done || "Saved");
      return true;
    } catch (e) {
      const t = errText(e);
      toast(fallback && t === "Something went wrong." ? fallback : t, { bad: true });
      return false;
    }
  };
  const setAdmin = (m: Member, on: boolean, undo = true) =>
    act(
      () => api.teams.setAdmin(slug, teamSlug, m.user_id, { admin: on }),
      () => toast(personName(m) + (on ? " is an admin now" : " is no longer an admin"), undo ? { undo: { label: "Undo", run: () => void setAdmin(m, !on, false) } } : undefined),
    );
  const setLocked = (on: boolean, undo = true) =>
    act(
      () => api.teams.lock(slug, teamSlug, { locked: on }),
      (r) => {
        const c = (r as { cleared?: { requests?: number; invites?: number } })?.cleared;
        const n = c ? (c.requests || 0) + (c.invites || 0) : 0;
        const text = (on ? team.name + " is locked." : team.name + " is open to join again.") + (n ? " " + n + " pending request" + (n === 1 ? "" : "s") + " and invites were cleared." : "");
        toast(text, undo ? { undo: { label: "Undo", run: () => void setLocked(!on, false) } } : undefined);
      },
    );
  const moveTo = async (m: Member, target: string, undo = true) => {
    const to = teams.find((t) => t.slug === target)?.name || "another team";
    const ok = await act(
      () => api.teams.addMember(slug, target, { user_id: m.user_id }),
      () =>
        toast(
          "Moved " + personName(m) + " to " + to,
          undo ? { undo: { label: "Undo", run: () => void act(() => api.teams.addMember(slug, teamSlug, { user_id: m.user_id }), "Moved " + personName(m) + " back", "Could not move them.") } } : undefined,
        ),
      "Could not move them.",
    );
    if (ok) setMoving(null);
  };

  const names = joinAnd(members.map((m) => m.display_name || "Someone"));
  return (
    <RunFrame slug={slug} screen={match.id}>
      {crumbs(team.name)}
      <div className="flex flex-wrap items-end gap-3">
        <div className="min-w-0 flex-1 [&>div]:mt-0">
          <PageTitle sub={(team.track?.name || "No track") + " · " + team.members.length + " of " + max + " people"}>
            {team.name} {team.locked && !team.kept && <Badge tone="down">Locked</Badge>}
          </PageTitle>
        </div>
        {entry && (
          <Button asChild>
            <AppLink to={entryHref}>View entry</AppLink>
          </Button>
        )}
      </div>
      <LockNote reason={lock} />
      {team.kept && <Notice title={`Everyone left after the deadline. ${team.name} keeps its entry, site and scores, and can’t take new members.`} />}

      <Section
        title="Members"
        note={team.kept ? "Nobody is on this team now." : "Anyone with the team link joins straight away. Admins approve requests to join. Make someone admin if the team has lost theirs."}
      >
        {!team.kept && (
          <>
            {!lock && (
              <Button className="mb-1" disabled={full || !solo.length} onClick={() => setAdding(true)}>
                <span aria-hidden="true">+</span> Add someone
              </Button>
            )}
            {full ? (
              <p className="text-sm text-ink-soft">{team.name} is full.</p>
            ) : !lock && !solo.length ? (
              <p className="text-sm text-ink-soft">Everyone in the event is already on a team.</p>
            ) : null}
            {team.locked && <Notice title="Locked by the team. You can still place people here." />}
            {members.length ? (
              <ul aria-label="Members" className="m-0 mt-2 list-none rounded-wobble border border-rule bg-card px-3 py-1">
                {members.map((m) => (
                  <li key={m.user_id} className="flex min-h-14 items-center gap-3 border-b border-rule py-2 last:border-b-0">
                    <span className="min-w-0 flex-1">
                      <span className="block font-medium text-ink">{m.display_name || "Someone"}</span>
                      {m.email && <span className="block text-sm text-ink-soft">{m.email}</span>}
                    </span>
                    {m.admin && <Badge tone="accent">Admin</Badge>}
                    {!lock && (
                      <Menu
                        label={"More for " + (m.display_name || "Someone")}
                        items={[
                          { label: "Move to another team…", onSelect: () => setMoving(m) },
                          m.admin ? { label: "Remove as admin", onSelect: () => void setAdmin(m, false) } : { label: "Make admin", onSelect: () => void setAdmin(m, true) },
                          { separator: true },
                          { label: "Take off team", onSelect: () => setRemoving(m), danger: true },
                        ]}
                      />
                    )}
                  </li>
                ))}
              </ul>
            ) : (
              <p className="text-ink-soft">No members.</p>
            )}
          </>
        )}
      </Section>

      <div className="mt-7 grid gap-5">
        <Card title="Team">
          <Rename
            key={team.name}
            team={team}
            disabled={!!lock}
            onSave={async (name) => {
              try {
                await api.teams.update(slug, teamSlug, { name });
                await reload();
                toast("Saved");
                return "";
              } catch (e) {
                const t = errText(e);
                return t === "Something went wrong." ? "Could not rename the team." : t;
              }
            }}
          />
        </Card>
        {showTrack && (
          <Card title="Track">
            <TrackPick key={team.track?.slug || ""} team={team} tracks={tracks} disabled={trackLocked} onSave={(track) => act(() => api.content.setTeamTrack(slug, { team: teamSlug, track })).then(() => {})} />
          </Card>
        )}
        <Card title="Messages">
          {msgQ.isError ? (
            <p className="text-ink-soft">Messages did not load.</p>
          ) : messages.length ? (
            <MessageRows messages={messages} event={ev} />
          ) : (
            <p className="text-ink-soft">No messages with this team yet.</p>
          )}
          {ev.stage !== "archived" && (
            <Button className="mt-3" disabled={!!d.admin_view || !!ev.taken_down} onClick={() => setMessaging(true)}>
              Message team
            </Button>
          )}
        </Card>
        {!team.kept && (
          <Card title="Joining">
            <Check checked={!!team.locked} disabled={!!lock} onChange={(on) => void setLocked(on)} hint="No more people can join by link, code, request or invite, and Fill teams skips it. You can still place people here.">
              Lock team
            </Check>
            <div className="mt-3 border-t border-rule pt-3">
              <p className="font-medium">Team link</p>
              <p className="text-sm text-ink-soft">Anyone in this event who opens it joins straight away.</p>
              <p className="mt-1.5 font-mono text-sm break-all">{linkText(team, location.origin)}</p>
              <div className="mt-2 flex flex-wrap gap-2.5">
                <CopyButton text={link} />
                <Button disabled={!!lock} onClick={() => setNewLink(true)}>
                  New link
                </Button>
              </div>
            </div>
          </Card>
        )}
        <Card title="Entry & site">
          <dl className="grid grid-cols-[5rem_minmax(0,1fr)] gap-y-2">
            <dt className="text-sm text-ink-soft">Entry</dt>
            <dd className="m-0">
              <Badge tone={ep.tone === "neutral" ? undefined : ep.tone}>{ep.text}</Badge>
            </dd>
            <dt className="text-sm text-ink-soft">Site</dt>
            <dd className="m-0">
              <Badge tone={sp.tone === "neutral" ? undefined : sp.tone}>{sp.text}</Badge>
            </dd>
          </dl>
          <Button className="mt-3" asChild>
            <AppLink to={entryHref}>View entry</AppLink>
          </Button>
        </Card>
        <Section title="Danger zone" className="mt-0 rounded-field border border-bad px-4 py-3">
          <p className="font-medium">Delete this team</p>
          <p className="text-sm text-ink-soft">Removes {team.name}, its entry and its site for good. Members stay in the event without a team.</p>
          <Button variant="danger" className="mt-3" disabled={!!lock} onClick={() => setDeleting(true)}>
            Delete team
          </Button>
        </Section>
      </div>

      <PickDialog
        open={adding}
        title={"Add someone to " + team.name}
        label="Solo participant"
        options={solo.map((p) => ({ value: p.user_id, label: personName(p) }))}
        confirm="Add"
        onClose={() => setAdding(false)}
        onPick={async (user) => {
          if (await act(() => api.teams.addMember(slug, teamSlug, { user_id: user }), "Saved", "Could not move them.")) setAdding(false);
        }}
      />
      {moving && roomFor(teams, max, teamSlug).length === 0 ? (
        <Dialog open onOpenChange={(o) => !o && setMoving(null)} title={"Move " + personName(moving)}>
          <p className="text-ink-soft">No other team has room.</p>
          <DialogActions>
            <Button variant="primary" onClick={() => setMoving(null)}>
              Close
            </Button>
          </DialogActions>
        </Dialog>
      ) : (
        <PickDialog
          open={!!moving}
          title={"Move " + (moving ? personName(moving) : "") + " to another team"}
          label="Team"
          options={roomFor(teams, max, teamSlug).map((t) => ({ value: t.slug, label: t.name }))}
          confirm="Move"
          onClose={() => setMoving(null)}
          onPick={(target) => moveTo(moving!, target)}
        />
      )}
      <AskDialog
        open={!!removing}
        onOpenChange={(o) => !o && setRemoving(null)}
        title={"Remove " + (removing ? personName(removing) : "") + "?"}
        body={removing ? `${personName(removing)} loses access to this team’s entry and site. The team link changes. Remove ${personName(removing)} from ${team.name}?` : ""}
        confirmLabel="Take off team"
        danger
        onConfirm={() => act(() => api.teams.removeMember(slug, teamSlug, removing!.user_id), "Saved", "Could not remove them")}
      />
      <AskDialog
        open={newLink}
        onOpenChange={setNewLink}
        title={"Make a new link for " + team.name + "?"}
        body="The old link and code stop working. Everyone on the team stays."
        confirmLabel="New link"
        danger
        onConfirm={() => act(() => api.teams.newLink(slug, teamSlug))}
      />
      <AskDialog
        open={deleting}
        onOpenChange={setDeleting}
        title={"Delete " + team.name + "?"}
        body={`This removes ${team.name}, its entry and its site.${names ? " " + names + " become" : " Members become"} unteamed and can join or start another team. This can’t be undone. Delete ${team.name}?`}
        confirmLabel="Delete team"
        danger
        onConfirm={async () => {
          try {
            await api.teams.remove(slug, teamSlug);
            await refresh(["org", slug, "teams"]);
            toast("Deleted " + team.name);
            go(href("organiser", slug, "teams"));
          } catch (e) {
            toast(errText(e) === "Something went wrong." ? "Could not delete the team" : errText(e), { bad: true });
          }
        }}
      />
      <TeamMessageDialog
        slug={slug}
        open={messaging}
        onOpenChange={setMessaging}
        teamIds={[team.id]}
        title={"Message " + team.name}
        onSent={(n) => {
          toast("Sent to " + n + (n === 1 ? " person" : " people"));
          void msgQ.refetch();
        }}
      />
    </RunFrame>
  );
}
