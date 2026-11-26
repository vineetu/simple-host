// Team (/e/{slug}/team?s=team): getting onto a team, then running it.
// No team: "How do you want to team up?" with the open ways (ask a team
// that's looking, join with a link or code, start one) and only the chosen
// way's panel; invites for the person above it. On a team: Members (the team
// link, requests, the members and their menus, the thread with the
// organisers), Find people (team finding on) and Settings (name, looking
// for, track, lock, new link, leave). Team admins get the admin functions.
import { useEffect, useMemo, useState, type ReactNode } from "react";
import { useSearchParams } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Badge } from "@kit/components/ui/badge";
import { Button } from "@kit/components/ui/button";
import { Check, RadioCards } from "@kit/components/ui/choice";
import { CopyButton } from "@kit/components/ui/copy";
import { AskDialog } from "@kit/components/ui/dialog";
import { EmptyState } from "@kit/components/ui/empty-state";
import { Field, Input, NativeSelect } from "@kit/components/ui/input";
import { Menu, type MenuItem } from "@kit/components/ui/menu";
import { Notice } from "@kit/components/ui/notice";
import { PageTitle, Section } from "@kit/components/ui/page-shell";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@kit/components/ui/tabs";
import { useToast } from "@kit/components/ui/toast";
import { cn } from "@kit/lib/utils";
import { useHack } from "@/lib/client";
import { possessive } from "@/lib/labels";
import { useEventDetail } from "@/app/data";
import { Loading } from "@/app/states";
import type { ScreenProps } from "@/routes/registry";
import {
  NO_FINDING,
  WAYS,
  evOf,
  firstWay,
  inviteLine,
  justPlaced,
  markSelfJoin,
  openLine,
  placedNote,
  rules,
  tabsFor,
  teamLink,
  teamOf,
  ways,
  type Finding,
  type Member,
  type Message,
  type MyTeam,
  type Rules,
  type Way,
} from "./data";
import { Thread } from "./Thread";
import { Walkthrough, markSeen, seenKey, wasSeen, type Step } from "./Walkthrough";
import { blocked, say, signedOut } from "./words";

const TOUR: Step[] = [
  { target: '[data-tour="team-ways"]', title: "Three ways onto a team", body: "Ask a team that’s looking, join with a link or code a teammate sent you, or start your own." },
  { target: '[data-tour="open-teams"]', title: "Ask to join", body: "Teams looking for people. Ask one, or several. Their admins say yes or no." },
  { target: '[data-tour="be-found"]', title: "Let teams find you", body: "List yourself with a one-line intro. Teams can invite you, and you choose." },
];
const TOUR_KEY = "sh-participant-team-tour-done";
let autoTourTried = false; // once per page load, as the old screen

/** Card is a titled box on this screen. */
export function Box({ title, sub, children, className, ...rest }: { title?: string; sub?: ReactNode; children?: ReactNode; className?: string } & Record<`data-${string}`, string>) {
  return (
    <section className={cn("mt-5 rounded-wobble border border-rule bg-card px-4 py-4 md:px-5", className)} {...rest}>
      {title && <h2 className="type-section">{title}</h2>}
      {sub && <p className="mt-0.5 text-sm text-ink-soft">{sub}</p>}
      {children}
    </section>
  );
}

/** Row is one line of a list: a name, a line under it, and its actions. */
export function Row({ name, sub, children }: { name: ReactNode; sub?: ReactNode; children?: ReactNode }) {
  return (
    <li className="flex min-h-14 flex-wrap items-center gap-x-3 gap-y-2 border-b border-rule py-2 last:border-b-0">
      <div className="min-w-0 flex-[1_1_12rem]">
        <p className="font-medium break-words">{name}</p>
        {sub && <p className="text-sm break-words text-ink-soft">{sub}</p>}
      </div>
      {children && <div className="flex flex-none flex-wrap items-center gap-2">{children}</div>}
    </li>
  );
}

export function List({ label, children, head }: { label: string; children: ReactNode; head?: string }) {
  return (
    <div className="mt-4">
      {head && <p className="mb-1 text-sm text-ink-soft">{head}</p>}
      <ul aria-label={label} className="m-0 list-none rounded-wobble border border-rule bg-card px-4 py-1">
        {children}
      </ul>
    </div>
  );
}

type Act = (fn: () => Promise<unknown>, o?: { joins?: "joined" | "started"; saved?: string; onError?: (msg: string) => void; undo?: { label: string; run: () => Promise<unknown> } }) => Promise<boolean>;

function useAct(slug: string, hasTeam: boolean): [Act, () => Promise<void>] {
  const qc = useQueryClient();
  const toast = useToast();
  const refresh = async () => {
    await Promise.all([
      qc.invalidateQueries({ queryKey: ["event", slug] }),
      qc.invalidateQueries({ queryKey: ["teams-open", slug] }),
      qc.invalidateQueries({ queryKey: ["team-thread", slug] }),
    ]);
  };
  const act: Act = async (fn, o = {}) => {
    try {
      await fn();
    } catch (err) {
      if (signedOut(err)) return false;
      if (blocked(err)) {
        toast(say(err), { bad: true });
        await refresh();
        return false;
      }
      if (o.onError) o.onError(say(err));
      else toast(say(err), { bad: true });
      return false;
    }
    if (!hasTeam && o.joins) markSelfJoin(slug, o.joins);
    if (o.saved)
      toast(o.saved, o.undo ? { undo: { label: o.undo.label, run: () => void act(o.undo!.run, { saved: "Undone" }) } } : undefined);
    await refresh();
    return true;
  };
  return [act, refresh];
}

/** Busy runs an action with its button disabled meanwhile. */
function useBusy() {
  const [busy, setBusy] = useState("");
  const run = (id: string, p: () => Promise<unknown>) => {
    setBusy(id);
    return p().finally(() => setBusy(""));
  };
  return [busy, run] as const;
}

export default function Team({ match }: ScreenProps) {
  const slug = match.slug!;
  const api = useHack();
  const d = useEventDetail(slug).data!;
  const team = teamOf(d);
  const [params, setParams] = useSearchParams();
  const finding = useQuery({
    queryKey: ["teams-open", slug],
    // Left at "team finding off" when it does not load, as the old screen.
    queryFn: () => api.teams.open(slug).then((r) => ({ ...NO_FINDING, ...(r as Partial<Finding>) }) as Finding, () => NO_FINDING),
  });
  const thread = useQuery({
    queryKey: ["team-thread", slug],
    queryFn: () => api.messages.myThread(slug).then((r) => ((r as { messages?: Message[] }).messages || []) as Message[], () => [] as Message[]),
    enabled: !!team,
  });
  const r = rules(d);
  const uid = String(d.me?.user_id || "anon");
  const f = finding.data;
  const noWay = !team && f && !r.canJoinByCode && !r.canStart && !(f.enabled && r.canDiscover);

  // The walkthrough: from Help (?walkthrough=1), or once for someone with no team while teams are open.
  const [tour, setTour] = useState(false);
  const key = seenKey(TOUR_KEY, uid);
  useEffect(() => {
    if (!f || noWay) return;
    if (params.get("walkthrough") === "1") {
      const next = new URLSearchParams(params);
      next.delete("walkthrough");
      setParams(next, { replace: true });
      setTour(true);
    } else if (!team && r.teamsOpen && !autoTourTried) {
      autoTourTried = true;
      if (!wasSeen(key)) setTour(true);
    }
  }, [f, noWay, params, setParams, team, r.teamsOpen, key]);
  const endTour = useMemo(
    () => () => {
      setTour(false);
      markSeen(key);
    },
    [key],
  );

  if (!f) return <Loading />;
  const removal = !team && typeof d.me?.team_removal_notice === "string" && d.me.team_removal_notice ? (
    <Notice tone="bad" title={d.me.team_removal_notice as string} />
  ) : null;
  if (!team && noWay)
    return (
      <>
        <PageTitle sub="You’re not on a team.">Team</PageTitle>
        {removal}
        <EmptyState title="Teams can’t be started or joined now">Entries are closed. If you should be on a team, ask the organiser.</EmptyState>
      </>
    );
  return (
    <>
      {team ? <OnTeam slug={slug} team={team} r={r} f={f} messages={thread.data || []} uid={uid} /> : <NoTeam slug={slug} r={r} f={f} removal={removal} />}
      <Walkthrough steps={TOUR} open={tour} onDone={endTour} />
    </>
  );
}

// ------------------------------------------------------------- no team ---

function NoTeam({ slug, r, f, removal }: { slug: string; r: Rules; f: Finding; removal: ReactNode }) {
  const api = useHack();
  const [act] = useAct(slug, false);
  const [busy, run] = useBusy();
  const open = ways(r, f);
  const [picked, setPicked] = useState<Way | "">("");
  const way = picked && open.includes(picked) ? picked : firstWay(open, f);
  // Typed values live here, so switching ways never loses them.
  const [code, setCode] = useState("");
  const [codeErr, setCodeErr] = useState("");
  const [name, setName] = useState("");
  const [nameErr, setNameErr] = useState("");
  const invites = f.enabled ? f.pending.filter((p) => p.kind === "invite") : [];
  const invitedBy = new Set(invites.map((p) => p.team));

  const join = () => {
    const c = code.trim();
    if (!c) return setCodeErr("Enter a team link or code.");
    setCodeErr("");
    void run("join", () => act(() => api.team.join(slug, { code: c }), { joins: "joined", onError: setCodeErr }));
  };
  const start = () => {
    const n = name.trim();
    if (!n) return setNameErr("Enter a team name.");
    setNameErr("");
    void run("start", () => act(() => api.teams.create(slug, { name: n }), { joins: "started", onError: setNameErr }));
  };

  return (
    <>
      <PageTitle sub="You’re not on a team yet.">Team</PageTitle>
      {removal}
      {invites.length > 0 && (
        <Section title="Invites for you">
          <List label="Invites for you">
            {invites.map((p) => (
              <Row key={p.id} name={p.team_name} sub={inviteLine(p, f)}>
                <Button variant="primary" disabled={!!busy} onClick={() => run("a" + p.id, () => act(() => api.team.answerRequest(slug, p.id, "accept"), { joins: "joined" }))}>
                  Accept
                </Button>
                <Button disabled={!!busy} onClick={() => run("d" + p.id, () => act(() => api.team.answerRequest(slug, p.id, "decline")))}>
                  Decline
                </Button>
              </Row>
            ))}
          </List>
        </Section>
      )}
      <Section title="How do you want to team up?">
        <div data-tour="team-ways">
          <RadioCards label="How do you want to team up?" value={way} onChange={setPicked} options={open.map((w) => ({ value: w, title: WAYS[w][0], hint: WAYS[w][1] }))} />
        </div>
      </Section>
      {way === "ask" && (
        <>
          <Section title="Teams looking for people" note="Ask as many as you like. When one says yes, your other requests are withdrawn." className="pt-1">
            <div data-tour="open-teams">
              {f.teams.filter((t) => !invitedBy.has(t.slug)).length ? (
                <List label="Teams looking for people">
                  {f.teams
                    .filter((t) => !invitedBy.has(t.slug))
                    .map((t) => {
                      const req = f.pending.find((p) => p.kind === "request" && p.team === t.slug);
                      return (
                        <Row key={t.slug} name={t.name} sub={openLine(t)}>
                          {req ? (
                            <Button disabled={!!busy} onClick={() => run("w" + req.id, () => act(() => api.team.withdrawRequest(slug, req.id)))}>
                              Withdraw request
                            </Button>
                          ) : (
                            <Button disabled={!!busy || t.full || !r.canJoinByCode} onClick={() => run("q" + t.slug, () => act(() => api.team.askToJoin(slug, t.slug)))}>
                              {t.full ? "Full" : "Ask to join"}
                            </Button>
                          )}
                        </Row>
                      );
                    })}
                </List>
              ) : (
                <EmptyState title="No teams looking right now">Check back later, or start your own.</EmptyState>
              )}
            </div>
          </Section>
          <BeFound slug={slug} f={f} />
        </>
      )}
      {way === "code" && (
        <Box title="Join with a link or code">
          <form
            className="mt-3"
            onSubmit={(e) => {
              e.preventDefault();
              join();
            }}
          >
            <Field id="join-code" label="Team link or code" error={codeErr} hint="Paste the whole link or type the 8-character code. Ask a teammate to send it from their Team page.">
              <Input
                id="join-code"
                autoComplete="off"
                spellCheck={false}
                autoCapitalize="off"
                placeholder={location.host + "/team/… or k7m2p9qa"}
                value={code}
                aria-invalid={!!codeErr || undefined}
                aria-describedby="join-code-msg"
                onChange={(e) => setCode(e.target.value)}
              />
            </Field>
            <div className="flex justify-end">
              <Button type="submit" variant={invites.length ? "secondary" : "primary"} disabled={busy === "join"}>
                Join team
              </Button>
            </div>
          </form>
        </Box>
      )}
      {way === "start" && (
        <Box title="Start a new team">
          <form
            className="mt-3"
            onSubmit={(e) => {
              e.preventDefault();
              start();
            }}
          >
            <Field id="start-name" label="Team name" error={nameErr} hint="You’ll be its admin. You can rename it later.">
              <Input id="start-name" maxLength={80} autoComplete="off" value={name} aria-invalid={!!nameErr || undefined} aria-describedby="start-name-msg" onChange={(e) => setName(e.target.value)} />
            </Field>
            <div className="flex justify-end">
              <Button type="submit" variant={invites.length ? "secondary" : "primary"} disabled={busy === "start"}>
                Start team
              </Button>
            </div>
          </form>
        </Box>
      )}
    </>
  );
}

function BeFound({ slug, f }: { slug: string; f: Finding }) {
  const api = useHack();
  const [act] = useAct(slug, false);
  const [on, setOn] = useState(f.looking);
  const [intro, setIntro] = useState(f.intro || "");
  const [busy, setBusy] = useState(false);
  return (
    <Box title="Let teams find you" sub="Teams see your name and this line, and can invite you. You choose whether to join." data-tour="be-found">
      <form
        className="mt-2"
        onSubmit={async (e) => {
          e.preventDefault();
          setBusy(true);
          await act(() => api.team.setLooking(slug, { looking: on, intro }), { saved: "Saved" });
          setBusy(false);
        }}
      >
        <Check checked={on} onChange={setOn}>
          List me as looking for a team
        </Check>
        <Field id="look-intro" label="A line about you" hint="Up to 120 characters.">
          <Input id="look-intro" maxLength={120} value={intro} aria-describedby="look-intro-msg" onChange={(e) => setIntro(e.target.value)} />
        </Field>
        <div className="flex justify-end">
          <Button type="submit" disabled={busy}>
            Save
          </Button>
        </div>
      </form>
    </Box>
  );
}

// ------------------------------------------------------------- on team ---

const TAB_LABEL = { members: "Members", people: "Find people", settings: "Settings" };

function OnTeam({ slug, team, r, f, messages, uid }: { slug: string; team: MyTeam; r: Rules; f: Finding; messages: Message[]; uid: string }) {
  const [params, setParams] = useSearchParams();
  const tabs = tabsFor(f);
  const asked = params.get("tab") || "";
  const tab = (tabs as string[]).includes(asked) ? (asked as (typeof tabs)[number]) : "members";
  useEffect(() => {
    if (asked === tab) return;
    const next = new URLSearchParams(params);
    next.set("tab", tab);
    setParams(next, { replace: true });
  }, [asked, tab, params, setParams]);
  const members = team.members || [];
  const placed = useMemo(() => justPlaced(uid, slug, team.slug), [uid, slug, team.slug]);
  const goTab = (v: string) => {
    if (v === tab) return;
    const next = new URLSearchParams(params);
    next.set("tab", v);
    setParams(next);
  };
  return (
    <>
      <PageTitle sub={(team.track ? team.track.name : "No track") + " · " + members.length + " of " + r.size + " people" + (r.isAdmin ? " · You’re an admin" : "")}>
        <span className="inline-flex flex-wrap items-center gap-2">
          {team.name}
          {team.locked && <Badge>Locked</Badge>}
        </span>
      </PageTitle>
      <Tabs
        value={tab}
        onValueChange={goTab}
        className="mt-5"
      >
        <TabsList aria-label="Team sections">
          {tabs.map((t) => (
            <TabsTrigger key={t} value={t} onClick={() => goTab(t)}>
              {TAB_LABEL[t]}
              {t === "members" && <span className="ml-1.5 text-sm text-ink-soft">{members.length}</span>}
              {t === "people" && f.solo.length > 0 && <span className="ml-1.5 text-sm text-ink-soft">{f.solo.length}</span>}
            </TabsTrigger>
          ))}
        </TabsList>
        <TabsContent value={tab} className="pt-0">
          {tab === "members" && <Members slug={slug} team={team} r={r} f={f} messages={messages} placed={placedNote(placed, team.name)} />}
          {tab === "people" && <People slug={slug} r={r} f={f} />}
          {tab === "settings" && <Settings slug={slug} team={team} r={r} />}
        </TabsContent>
      </Tabs>
    </>
  );
}

function LinkCard({ slug, team, r }: { slug: string; team: MyTeam; r: Rules }) {
  const api = useHack();
  const [act] = useAct(slug, true);
  const [busy, setBusy] = useState(false);
  const { addr, link } = teamLink(team);
  const n = (team.members || []).length;
  if (r.frozen)
    return (
      <Box title="Team link" sub="Entries are closed, so nobody else can join. The code stays here as a record.">
        <p className="mt-2 font-mono text-sm break-all">{addr}</p>
      </Box>
    );
  if (team.locked)
    return (
      <Notice
        title={team.name + " is locked. No more people can join, by link, code, request or invite."}
        action={
          r.isAdmin ? (
            <Button
              disabled={busy}
              onClick={async () => {
                setBusy(true);
                await act(() => api.team.lock(slug, { locked: false }), { saved: team.name + " is unlocked." });
                setBusy(false);
              }}
            >
              Unlock team
            </Button>
          ) : undefined
        }
      >
        {r.isAdmin ? undefined : "Ask an admin to unlock it."}
      </Notice>
    );
  return (
    <Box title="Team link" sub={r.full ? team.name + " is full, " + n + " of " + r.size + ". Nobody else can join until someone leaves." : "Anyone in this event who opens it joins " + team.name + " straight away."}>
      <div className="mt-3 flex flex-wrap items-center gap-3">
        <span className="min-w-0 flex-[1_1_14rem] font-mono text-sm break-all">{addr}</span>
        <CopyButton text={link} label="Copy link" variant="primary" />
      </div>
      {team.code && (
        <p className="mt-2 text-sm text-ink-soft">
          Or share the code <span className="font-mono text-ink">{team.code}</span>.
        </p>
      )}
    </Box>
  );
}

function Members({ slug, team, r, f, messages, placed }: { slug: string; team: MyTeam; r: Rules; f: Finding; messages: Message[]; placed: string }) {
  const api = useHack();
  const [act] = useAct(slug, true);
  const [busy, run] = useBusy();
  const [ask, setAsk] = useState<null | "leave" | Member>(null);
  const members = team.members || [];
  const requests = f.pending.filter((p) => p.kind === "request");
  const intro = r.frozen || team.locked ? "" : r.full ? team.name + " is full." : "Anyone with the team link joins straight away. Admins approve requests to join.";
  const memberItems = (m: Member): MenuItem[] | null => {
    if (!r.canAct) return null;
    const name = m.display_name || "this person";
    if (r.isAdmin && m.you)
      return [
        { label: "Step down as admin", onSelect: () => void act(() => api.team.setAdmin(slug, m.user_id, { admin: false }), { saved: "You’re not an admin now." }) },
        { separator: true },
        { label: "Leave team", onSelect: () => setAsk("leave"), danger: true },
      ];
    if (r.isAdmin)
      return [
        m.admin
          ? {
              label: "Remove as admin",
              onSelect: () =>
                void act(() => api.team.setAdmin(slug, m.user_id, { admin: false }), {
                  saved: name + " is not an admin now.",
                  undo: { label: "Undo", run: () => api.team.setAdmin(slug, m.user_id, { admin: true }) },
                }),
            }
          : {
              label: "Make admin",
              onSelect: () =>
                void act(() => api.team.setAdmin(slug, m.user_id, { admin: true }), {
                  saved: name + " is an admin now.",
                  undo: { label: "Undo", run: () => api.team.setAdmin(slug, m.user_id, { admin: false }) },
                }),
            },
        { separator: true },
        { label: "Remove from team", onSelect: () => setAsk(m), danger: true },
      ];
    if (m.you) return [{ label: "Leave team", onSelect: () => setAsk("leave"), danger: true }];
    return null;
  };
  return (
    <>
      {placed && <Notice title={placed} />}
      <Section title="Members" note={intro || undefined}>
        {r.frozen && <Notice title={"Entries are closed, so " + team.name + " can’t change now."} />}
        <LinkCard slug={slug} team={team} r={r} />
        {!r.frozen && requests.length > 0 && (
          <List label="Asked to join" head={"Asked to join · " + requests.length}>
            {requests.map((p) => (
              <Row key={p.id} name={p.display_name} sub="Asked to join">
                {r.isAdmin ? (
                  <>
                    {r.full ? (
                      <Button disabled>Full</Button>
                    ) : (
                      <Button variant="primary" disabled={!!busy} onClick={() => run("a" + p.id, () => act(() => api.team.answerRequest(slug, p.id, "accept")))}>
                        Accept
                      </Button>
                    )}
                    <Button disabled={!!busy} onClick={() => run("d" + p.id, () => act(() => api.team.answerRequest(slug, p.id, "decline")))}>
                      Decline
                    </Button>
                  </>
                ) : (
                  <span className="text-sm text-ink-soft">Waiting for an admin</span>
                )}
              </Row>
            ))}
          </List>
        )}
        <List label="Members" head={"Members · " + members.length}>
          {members.map((m) => {
            const items = memberItems(m);
            return (
              <Row key={m.user_id} name={m.display_name || "Someone"} sub={m.you ? "You" : undefined}>
                {m.admin && <Badge tone="accent">Admin</Badge>}
                {items && <Menu label={"More for " + (m.you ? "yourself" : m.display_name || "this person")} items={items} />}
              </Row>
            );
          })}
        </List>
        <p className="mt-2 text-sm text-ink-soft">
          {members.length} of {r.size} people.{r.isAdmin && r.canAct && !team.locked ? " Lock the team in Settings when it’s complete." : ""}
        </p>
      </Section>
      <Thread slug={slug} ended={r.ended} messages={messages} />
      <LeaveDialog slug={slug} team={team} open={ask === "leave"} onOpenChange={(o) => !o && setAsk(null)} />
      <AskDialog
        open={!!ask && ask !== "leave"}
        onOpenChange={(o) => !o && setAsk(null)}
        title={"Remove " + ((ask as Member)?.display_name || "this person") + "?"}
        body={((ask as Member)?.display_name || "This person") + " loses access to " + possessive(team.name) + " entry and website. The team link changes, so the old one stops working."}
        confirmLabel="Remove from team"
        danger
        onConfirm={() => act(() => api.team.removeMember(slug, (ask as Member).user_id), { saved: ((ask as Member).display_name || "They") + " left " + team.name + "." })}
      />
    </>
  );
}

function People({ slug, r, f }: { slug: string; r: Rules; f: Finding }) {
  const api = useHack();
  const [act] = useAct(slug, true);
  const [busy, run] = useBusy();
  return (
    <Section title="Find people" note="People in this event looking for a team. Invite someone and they choose whether to join.">
      {!r.canInvite && !r.frozen && <Notice title="Your team is locked. No more people can join, by link, code, request or invite." />}
      {f.solo.length ? (
        <List label="People looking for a team">
          {f.solo.map((p) => {
            const pend = f.pending.find((x) => x.kind === "invite" && x.user_id === p.user_id);
            return (
              <Row key={p.user_id} name={p.display_name} sub={p.intro || undefined}>
                {pend ? (
                  <Button disabled={!!busy} onClick={() => run("c" + pend.id, () => act(() => api.team.withdrawRequest(slug, pend.id), { saved: "Invite cancelled" }))}>
                    Cancel invite
                  </Button>
                ) : (
                  <Button disabled={!!busy || !r.canInvite || r.full} onClick={() => run("i" + p.user_id, () => act(() => api.team.invite(slug, { user_id: p.user_id }), { saved: "Invited " + p.display_name }))}>
                    {r.full ? "Full" : "Invite"}
                  </Button>
                )}
              </Row>
            );
          })}
        </List>
      ) : (
        <EmptyState title="Nobody is looking right now">People who list themselves as looking for a team show here.</EmptyState>
      )}
      <p className="mt-2 text-sm text-ink-soft">Anyone on the team can invite. An invite ends when they join any team.</p>
    </Section>
  );
}

function Settings({ slug, team, r }: { slug: string; team: MyTeam; r: Rules }) {
  const api = useHack();
  const d = useEventDetail(slug).data!;
  const tracks = evOf(d).tracks || [];
  const [act] = useAct(slug, true);
  const [name, setName] = useState(team.name);
  const [looking, setLooking] = useState(team.looking_for || "");
  const [nameErr, setNameErr] = useState("");
  const [busy, setBusy] = useState("");
  const [ask, setAsk] = useState<"" | "lock" | "link" | "leave">("");
  const dirty = name !== team.name || looking !== (team.looking_for || "");
  const members = team.members || [];
  const admins = members.filter((m) => m.admin).map((m) => m.display_name || "Someone");
  const pending = team.pending_requests || 0;
  const lock = async (locked: boolean) => {
    setBusy("lock");
    await act(() => api.team.lock(slug, { locked }), {
      saved: locked ? team.name + " is locked." : team.name + " is unlocked.",
      // Unlocking brings nothing back that locking cleared, so only a lock with nothing pending is undone this way.
      undo: locked && !pending ? { label: "Undo", run: () => api.team.lock(slug, { locked: false }) } : undefined,
    });
    setBusy("");
  };
  return (
    <>
      <Section title="Settings" note={r.isAdmin ? "Admins change the team’s name, what it’s looking for, the lock and its link." : "Settings for " + team.name + "."}>
        {r.isAdmin ? (
          <Box title="Team">
            <form
              className="mt-3"
              onSubmit={async (e) => {
                e.preventDefault();
                const n = name.trim();
                if (!n) return setNameErr("Enter a team name.");
                setNameErr("");
                setBusy("save");
                await act(() => api.team.update(slug, { name: n, looking_for: looking }), { saved: "Saved", onError: setNameErr });
                setBusy("");
              }}
            >
              <Field id="set-name" label="Team name" error={nameErr}>
                <Input id="set-name" maxLength={80} value={name} disabled={!r.canAct} aria-invalid={!!nameErr || undefined} aria-describedby="set-name-msg" onChange={(e) => setName(e.target.value)} />
              </Field>
              <Field id="set-looking" label="Looking for" hint="Shown to people browsing teams. Leave it empty if you’re not looking.">
                <Input id="set-looking" maxLength={120} value={looking} disabled={!r.canAct} aria-describedby="set-looking-msg" onChange={(e) => setLooking(e.target.value)} />
              </Field>
              <div className="flex flex-wrap items-center justify-end gap-2">
                {!dirty && <span className="mr-auto text-sm text-ink-soft">No changes</span>}
                <Button
                  disabled={!dirty}
                  onClick={() => {
                    setName(team.name);
                    setLooking(team.looking_for || "");
                    setNameErr("");
                  }}
                >
                  Discard
                </Button>
                <Button type="submit" variant="primary" disabled={!dirty || !r.canAct || busy === "save"}>
                  Save
                </Button>
              </div>
            </form>
          </Box>
        ) : (
          <Box title="Team">
            <dl className="mt-2 grid grid-cols-1 gap-x-4 gap-y-1 md:grid-cols-[8rem_minmax(0,1fr)]">
              <dt className="text-sm text-ink-soft">Team name</dt>
              <dd className="m-0">{team.name}</dd>
              <dt className="text-sm text-ink-soft">Looking for</dt>
              <dd className="m-0">{team.looking_for || "Not looking"}</dd>
            </dl>
            <p className="mt-2 text-sm text-ink-soft">
              Only admins change these.{admins.length ? " Admins: " + admins.join(", ") + "." : ""}
            </p>
          </Box>
        )}
        {r.isAdmin && !r.frozen && (
          <Box title="Joining">
            <div className="mt-3 flex flex-wrap items-center gap-3 border-b border-rule pb-3">
              <div className="min-w-0 flex-[1_1_16rem]">
                <p className="font-medium">Lock team</p>
                <p className="text-sm text-ink-soft">No more people can join. The team link, requests and invites stop working, and the organiser’s Fill teams skips {team.name}.</p>
              </div>
              <Button disabled={busy === "lock"} onClick={() => (team.locked ? lock(false) : pending ? setAsk("lock") : lock(true))}>
                {team.locked ? "Unlock team" : "Lock team"}
              </Button>
            </div>
            <div className="mt-3 flex flex-wrap items-center gap-3">
              <div className="min-w-0 flex-[1_1_16rem]">
                <p className="font-medium">New team link</p>
                <p className="text-sm text-ink-soft">The old link and code stop working. People already on the team stay.</p>
              </div>
              <Button onClick={() => setAsk("link")}>New link</Button>
            </div>
          </Box>
        )}
        {tracks.length > 0 && (
          <Box title="Track" sub={"Which challenge " + team.name + " is entering. Anyone on the team can change it."}>
            <NativeSelect
              aria-label="Track"
              className="mt-3 w-full md:w-80"
              disabled={!r.canAct}
              value={team.track?.slug || ""}
              onChange={(e) => {
                const v = e.target.value;
                void act(() => api.content.setTeamTrack(slug, { track: v }), { saved: "Saved" });
              }}
            >
              <option value="">No track</option>
              {tracks.map((t) => (
                <option key={t.slug} value={t.slug}>
                  {t.name}
                </option>
              ))}
            </NativeSelect>
          </Box>
        )}
        {!r.frozen && (
          <section className="mt-5 rounded-wobble border border-bad px-4 py-4 md:px-5">
            <h2 className="type-section">Leave team</h2>
            <div className="mt-1 flex flex-wrap items-center gap-3">
              <p className="min-w-0 flex-[1_1_16rem] text-sm text-ink-soft">
                {members.length === 1
                  ? "You’re the only member, so " + team.name + ", its entry and its website are deleted when you leave."
                  : "You’ll lose access to " + possessive(team.name) + " entry and website." + (team.admin_successor ? " " + team.admin_successor + " becomes admin when you leave." : "")}
              </p>
              <Button variant="danger" onClick={() => setAsk("leave")}>
                {members.length === 1 ? "Delete team and leave" : "Leave team"}
              </Button>
            </div>
          </section>
        )}
      </Section>
      <AskDialog
        open={ask === "lock"}
        onOpenChange={(o) => !o && setAsk("")}
        title={"Lock " + team.name + "?"}
        body={"No more people can join. " + pending + " pending request" + (pending === 1 ? "" : "s") + " and invites will be cleared."}
        confirmLabel="Lock team"
        danger
        onConfirm={() => lock(true)}
      />
      <AskDialog
        open={ask === "link"}
        onOpenChange={(o) => !o && setAsk("")}
        title="Make a new team link?"
        body="The old link and code stop working. People already on the team stay."
        confirmLabel="New link"
        danger
        onConfirm={() => act(() => api.team.newLink(slug), { saved: "New link made" })}
      />
      <LeaveDialog slug={slug} team={team} open={ask === "leave"} onOpenChange={(o) => !o && setAsk("")} />
    </>
  );
}

function LeaveDialog({ slug, team, open, onOpenChange }: { slug: string; team: MyTeam; open: boolean; onOpenChange: (o: boolean) => void }) {
  const api = useHack();
  const [act] = useAct(slug, true);
  const only = (team.members || []).length === 1;
  return (
    <AskDialog
      open={open}
      onOpenChange={onOpenChange}
      title={only ? "Delete " + team.name + " and leave?" : "Leave " + team.name + "?"}
      body={
        only
          ? "You’re the only member, so " + team.name + ", its entry and its website are deleted for good."
          : "You’ll lose access to its entry and website." + (team.admin_successor ? " " + team.admin_successor + " becomes admin." : "") + " You can join or start another team while teams are open."
      }
      confirmLabel={only ? "Delete team and leave" : "Leave team"}
      danger
      onConfirm={() => act(() => api.team.leave(slug), { saved: only ? team.name + " was deleted." : "You left " + team.name + "." })}
    />
  );
}
