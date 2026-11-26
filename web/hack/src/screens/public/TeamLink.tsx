// Team link (/team/{code}), signed in or out: one confirm joins the team, or
// the page says plainly why it can't (not in the event, waiting, on another
// team, locked, full, teams closed). Never joins on the bare page load (link
// previews). Joining is a full page load into the team's Members tab, which
// says "You joined …" (public-team-link.js, the sh-selfjoin flag).
import { useState } from "react";
import { Button } from "@kit/components/ui/button";
import { Card } from "@kit/components/ui/card";
import { FieldMessage } from "@kit/components/ui/input";
import type { ApiError } from "@kit/api/call";
import { useHack } from "@/lib/client";
import { href } from "@/routes/resolve";
import type { ScreenProps } from "@/routes/registry";
import { JOIN_ERRORS, OFFLINE_LOAD, errText, isGone, isOffline, teamLinkCase, type TeamLinkInfo } from "./data";
import { Actions, LinkButton, LinkLoading, Page, Problem, SignInStep, eventPage, useAccount, useLink } from "./parts";

function Answer({ title, sub, children }: { title: string; sub?: string; children?: React.ReactNode }) {
  return (
    <Card>
      <h2 className="type-section">{title}</h2>
      {sub && <p className="mt-1.5 text-ink-soft">{sub}</p>}
      {children && <Actions>{children}</Actions>}
    </Card>
  );
}

function Ready({ code, info, teamHref }: { code: string; info: TeamLinkInfo; teamHref: string }) {
  const api = useHack();
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const team = info.team || {};
  const slug = info.event?.slug || "";
  const name = team.name || "this team";
  const join = async () => {
    setBusy(true);
    setErr("");
    try {
      await api.team.join(slug, { code });
      try {
        sessionStorage.setItem("sh-selfjoin-" + slug, "joined");
      } catch {
        /* storage blocked: the team screen just skips its line */
      }
      location.assign(teamHref + "&tab=members");
    } catch (e) {
      setBusy(false);
      if ((e as ApiError).status !== 401) setErr(errText(e, JOIN_ERRORS));
    }
  };
  const facts: [string, string][] = [
    ["Team", team.name || ""],
    ["People", (team.member_count ?? "") + " of " + (team.team_size_max ?? "")],
  ];
  if (team.track?.name) facts.push(["Track", team.track.name]);
  return (
    <Card>
      <h2 className="type-section">You’re about to join {name}</h2>
      <p className="mt-1.5 text-ink-soft">You’ll be on the team straight away and can work on its entry and website. You can leave while teams are open.</p>
      <dl className="mt-4 grid grid-cols-[auto_1fr] gap-x-6 gap-y-1">
        {facts.map(([k, v]) => (
          <div key={k} className="contents">
            <dt className="text-ink-soft">{k}</dt>
            <dd className="m-0">{v}</dd>
          </div>
        ))}
      </dl>
      {err && <FieldMessage tone="bad">{err}</FieldMessage>}
      <Actions>
        <Button variant="primary" onClick={join} disabled={busy}>
          Join {name}
        </Button>
        <LinkButton href="/events">Not now</LinkButton>
      </Actions>
    </Card>
  );
}

export default function TeamLink({ match }: ScreenProps) {
  const code = match.code || "";
  const api = useHack();
  const acct = useAccount();
  const q = useLink(["team-link", code], () => api.invites.teamLink(code) as Promise<TeamLinkInfo>);

  if (q.isPending) return <LinkLoading label="Loading the team link" />;
  if (q.isError) {
    if (isGone(q.error, "team_link_not_found"))
      return (
        <Page title="Team link" lines={[location.hostname]}>
          <Answer title="This team link doesn’t work" sub="The team may have made a new link, or the link was copied wrong. Ask a teammate to send it again.">
            <LinkButton primary href="/directory">
              Find a hackathon
            </LinkButton>
          </Answer>
        </Page>
      );
    return <Problem text={isOffline(q.error) ? OFFLINE_LOAD : errText(q.error, JOIN_ERRORS)} />;
  }
  const info = q.data;
  const ev = info.event || {};
  const team = info.team?.name || "this team";
  const evTitle = ev.title || "this event";
  const slug = ev.slug || "";
  const open = eventPage(slug);
  const teamHref = href("participant", slug, "team");
  const page = (body: React.ReactNode) => (
    <Page title={"Join " + team} lines={["A team in " + evTitle]}>
      {body}
    </Page>
  );
  const openEvent = (
    <LinkButton primary href={open}>
      Open the event
    </LinkButton>
  );
  const findTeam = (
    <LinkButton primary href={teamHref}>
      Find a team
    </LinkButton>
  );
  const goTeam = (
    <LinkButton primary href={teamHref}>
      Go to your team
    </LinkButton>
  );
  switch (teamLinkCase(info, acct.signedIn)) {
    case "signed-out":
      return page(
        <SignInStep title={"Sign in to join " + team}>
          <p className="text-ink-soft">Use the account you joined {evTitle} with. You’ll come straight back here.</p>
        </SignInStep>,
      );
    case "staff":
      return page(<Answer title="Only participants join teams" sub="Organisers and judges don’t join teams.">{openEvent}</Answer>);
    case "pending":
      return page(<Answer title="Your place in the event is waiting" sub="An organiser still has to approve you. Open this link again once you’re in." />);
    case "rejected":
      return page(<Answer title="You’re not in this event" sub="Ask the organiser if you have questions.">{openEvent}</Answer>);
    case "not_in_event":
      return page(
        <Answer
          title="Join the event first"
          sub={`This link adds you to a team in ${evTitle}, and you’re not in that event yet.` + (ev.applications_open ? "" : " Ask the organiser for the event’s join link.")}
        >
          {ev.applications_open && (
            <LinkButton primary href={open + "/apply"}>
              Apply to the event
            </LinkButton>
          )}
          <LinkButton href={open}>Open the event</LinkButton>
        </Answer>,
      );
    case "on_this_team":
      return page(<Answer title={"You’re already on " + team}>{goTeam}</Answer>);
    case "on_other_team": {
      const mine = info.you?.team_name;
      return page(
        <Answer title={"You’re on " + (mine || "another team")} sub={`To join ${team}, leave ${mine || "your team"} first, then open this link again.`}>
          {goTeam}
        </Answer>,
      );
    }
    case "locked":
      return page(<Answer title={(info.team?.name || "This team") + " isn’t taking new people"} sub="Its admins locked the team. Ask them, or find another team.">{findTeam}</Answer>);
    case "full":
      return page(
        <Answer
          title={(info.team?.name || "This team") + " is full"}
          sub={`It has ${info.team?.member_count ?? ""} of ${info.team?.team_size_max ?? ""} people. Find another team or start your own.`}
        >
          {findTeam}
        </Answer>,
      );
    case "teams_closed":
      return page(<Answer title="Teams can’t change now" sub="Entries are closed for this event.">{openEvent}</Answer>);
  }
  return page(<Ready code={code} info={info} teamHref={teamHref} />);
}
