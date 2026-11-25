// Teams: each team's members and invitations, inviting by email, leaving or
// removing someone (asked first: they lose the team's sites), deleting an
// empty team, and making a new one. Invitations to me are joined here too.
import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useApi } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { AskDialog } from "@/components/ui/dialog";
import { FieldMessage, Input, Label } from "@/components/ui/input";
import { Notice } from "@/components/ui/notice";
import { useToast } from "@/components/ui/toast";
import { validName, type Me, type Team } from "../data";
import { Part, Row } from "../bits";

type Asking = { kind: "remove"; team: Team; id: string; email: string } | { kind: "delete"; team: Team } | null;

export function Teams({ me }: { me: Me }) {
  const call = useApi();
  const qc = useQueryClient();
  const toast = useToast();
  const [asking, setAsking] = useState<Asking>(null);
  const [handle, setHandle] = useState("");
  const [error, setError] = useState("");
  const run = async (method: string, path: string, body: unknown, done: string) => {
    try {
      await call(method, path, body);
      await qc.invalidateQueries();
      toast(done);
      return true;
    } catch (e) {
      toast((e as Error).message, { bad: true });
      return false;
    }
  };
  const t = (team: Team) => "/v1/teams/" + encodeURIComponent(team.handle);

  return (
    <>
      {me.invitations.map((i) => (
        <Notice
          key={i.id}
          title={"The team " + (i.display_name || i.team) + " invited you"}
          action={<Button onClick={() => run("POST", "/v1/invites/" + encodeURIComponent(i.id) + "/accept", {}, "You joined " + (i.display_name || i.team) + ".")}>Join</Button>}
        />
      ))}
      <p className="mt-2 max-w-[70ch] text-sm text-ink-soft">
        A team has its own name and its own sites. Every member manages them with their own sign-in, and its sites show in each member's list.
      </p>
      {me.teams.map((team) => (
        <TeamPart key={team.handle} me={me} team={team} run={run} path={t(team)} ask={setAsking} />
      ))}
      <Part title="Make a team">
        <form
          noValidate
          onSubmit={async (e) => {
            e.preventDefault();
            const h = handle.trim().toLowerCase();
            if (!validName(h)) return setError("Use lower-case letters, digits and single hyphens, like bakery-team.");
            setError("");
            if (await run("POST", "/v1/teams", { handle: h }, "Made the team " + h + ". Invite people below it.")) setHandle("");
          }}
        >
          <Label htmlFor="team-handle">The team's name</Label>
          <div className="flex flex-wrap gap-2">
            <Input id="team-handle" className="flex-[1_1_14rem]" value={handle} onChange={(e) => (setHandle(e.target.value), setError(""))} autoComplete="off" autoCapitalize="none" spellCheck={false} aria-invalid={!!error} />
            <Button type="submit" variant="primary" className="px-5">
              Make team
            </Button>
          </div>
          {error && <FieldMessage tone="bad">{error}</FieldMessage>}
          <p className="mt-1.5 text-sm text-ink-soft">It is part of the team's site addresses, like a person's name.</p>
        </form>
      </Part>
      <AskDialog
        open={!!asking}
        onOpenChange={(o) => !o && setAsking(null)}
        title={
          asking?.kind === "delete"
            ? "Delete the team " + asking.team.handle + "?"
            : asking?.id === me.id
              ? "Leave " + asking.team.handle + "?"
              : "Remove " + (asking?.email || "") + " from " + (asking?.team.handle || "") + "?"
        }
        body={
          asking?.kind === "delete"
            ? "Only a team with no sites can be deleted. Its name is retired."
            : asking?.id === me.id
              ? "You can no longer open or change its sites. A member can invite you back."
              : "They can no longer open or change the team's sites."
        }
        confirmLabel={asking?.kind === "delete" ? "Delete team" : asking?.id === me.id ? "Leave" : "Remove"}
        danger
        onConfirm={async () => {
          if (asking?.kind === "delete") await run("DELETE", t(asking.team), undefined, "Deleted the team " + asking.team.handle + ".");
          else if (asking)
            await run(
              "DELETE",
              t(asking.team) + "/members/" + encodeURIComponent(asking.id),
              undefined,
              asking.id === me.id ? "You left " + asking.team.handle + "." : "Removed " + asking.email + ".",
            );
        }}
      />
    </>
  );
}

function TeamPart({
  me,
  team,
  run,
  path,
  ask,
}: {
  me: Me;
  team: Team;
  run: (m: string, p: string, b: unknown, done: string) => Promise<boolean>;
  path: string;
  ask: (a: Asking) => void;
}) {
  const [email, setEmail] = useState("");
  const [error, setError] = useState("");
  return (
    <Part title={team.display_name ? team.display_name + " (" + team.handle + ")" : team.handle}>
      <ul className="m-0 mb-4 list-none p-0" aria-label={"Members of " + team.handle}>
        {team.members.map((m) => (
          <Row
            key={m.id}
            end={
              <Button variant="ghost" className="px-3 text-bad" onClick={() => ask({ kind: "remove", team, id: m.id, email: m.email })}>
                {m.id === me.id ? "Leave" : "Remove"}
              </Button>
            }
          >
            <span className="break-all">{m.email}</span>
            {m.id === me.id && <span className="text-sm text-ink-soft"> (you)</span>}
          </Row>
        ))}
        {team.invites.map((i) => (
          <Row key={i.email}>
            <span className="break-all text-ink-soft">{i.email}</span> <span className="text-sm text-ink-soft">· invited, not joined yet</span>
          </Row>
        ))}
      </ul>
      <form
        noValidate
        onSubmit={async (e) => {
          e.preventDefault();
          const v = email.trim().toLowerCase();
          if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(v)) return setError("That email address does not look right.");
          setError("");
          if (await run("POST", path + "/invites", { email: v }, "Invitation sent to " + v + ". They join when they accept.")) setEmail("");
        }}
      >
        <Label htmlFor={"invite-" + team.handle}>Invite someone by email</Label>
        <div className="flex flex-wrap gap-2">
          <Input id={"invite-" + team.handle} type="email" className="flex-[1_1_14rem]" value={email} onChange={(e) => (setEmail(e.target.value), setError(""))} placeholder="name@example.com" autoComplete="off" aria-invalid={!!error} />
          <Button type="submit">Invite</Button>
        </div>
        {error && <FieldMessage tone="bad">{error}</FieldMessage>}
      </form>
      <Button variant="ghost" className="mt-3 px-3 text-bad" onClick={() => ask({ kind: "delete", team })}>
        Delete the team
      </Button>
    </Part>
  );
}
