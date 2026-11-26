// Judge invite (/judge/{code}), signed in or out: the event and its code of
// conduct, then sign in, then accept. A dead link says why and offers a way
// on; a closed one names its stage; someone already in is sent to the event.
// Accepting is a full page load into the judge area, whose page has its own
// CSP (public-judge.js, platform.js judgeJoined).
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Button } from "@kit/components/ui/button";
import { Card } from "@kit/components/ui/card";
import { Check } from "@kit/components/ui/choice";
import { FieldMessage } from "@kit/components/ui/input";
import type { ApiError } from "@kit/api/call";
import { useHack } from "@/lib/client";
import { roleLabel } from "@/lib/labels";
import type { ScreenProps } from "@/routes/registry";
import { OFFLINE_LOAD, accountName, checkJoin, closedLine, errText, isGone, isOffline, joinBody, judgeDates, type JudgeInfo } from "./data";
import { Actions, Coc, LinkButton, LinkLoading, NameLine, Page, Problem, SignInStep, eventPage, useAccount, useLink } from "./parts";

const judgeArea = (slug: string) => eventPage(slug) + "/judge";

function Already({ info, role }: { info: JudgeInfo; role?: string }) {
  return (
    <Page title={"You’re already in " + (info.title || "this event")} lines={["Role: " + (roleLabel(role) || "Judge")]}>
      <Actions>
        <LinkButton primary href={judgeArea(info.slug)}>
          Open it
        </LinkButton>
      </Actions>
    </Page>
  );
}

function Accept({ code, info, name }: { code: string; info: JudgeInfo; name: string }) {
  const api = useHack();
  const [typed, setTyped] = useState("");
  const [coc, setCoc] = useState(false);
  const [errs, setErrs] = useState<{ coc?: string; name?: string; form?: string }>({});
  const [busy, setBusy] = useState(false);
  const [already, setAlready] = useState(false);
  if (already)
    return (
      <Card className="mt-6">
        <h2 className="type-section">You’re already in {info.title || "this event"}</h2>
        <Actions>
          <LinkButton primary href={judgeArea(info.slug)}>
            Open it
          </LinkButton>
        </Actions>
      </Card>
    );
  const send = async (e: React.FormEvent) => {
    e.preventDefault();
    const found = checkJoin({ coc, askName: !name, name: typed });
    setErrs(found);
    if (found.coc || found.name) return;
    setBusy(true);
    try {
      const r = (await api.invites.acceptJudge(code, joinBody({ askName: !name, name: typed, answers: {} }))) as { slug?: string; event?: { slug?: string } };
      location.assign(judgeArea(r.slug || r.event?.slug || info.slug));
    } catch (err) {
      setBusy(false);
      if ((err as ApiError).code === "already_member") setAlready(true);
      else if ((err as ApiError).status !== 401) setErrs({ form: errText(err) });
    }
  };
  return (
    <Card className="mt-6">
      <form noValidate onSubmit={send}>
        <h2 className="mb-4 type-section">Accept invitation</h2>
        <NameLine name={name} id="judge-name" value={typed} onChange={setTyped} error={errs.name} />
        <Check checked={coc} onChange={setCoc}>
          I agree to the code of conduct
        </Check>
        {errs.coc && <FieldMessage tone="bad">{errs.coc}</FieldMessage>}
        {errs.form && <FieldMessage tone="bad">{errs.form}</FieldMessage>}
        <Button type="submit" variant="primary" size="lg" className="mt-5 md:w-auto md:px-8" disabled={busy}>
          Join as judge
        </Button>
      </form>
    </Card>
  );
}

export default function Judge({ match }: ScreenProps) {
  const code = match.code || "";
  const api = useHack();
  const acct = useAccount();
  const q = useLink(["judge", code], () => api.invites.judge(code) as Promise<JudgeInfo>);
  const slug = q.data?.slug;
  const member = !!slug && !!acct.events?.some((e) => e.slug === slug);
  const mine = useQuery({ queryKey: ["event", slug], queryFn: () => api.event.get(slug!), enabled: member, retry: false });

  if (q.isPending || !acct.ready || (member && mine.isPending)) return <LinkLoading label="Loading the invitation" />;
  if (q.isError) {
    if (isGone(q.error, "invalid_code"))
      return (
        <Page title="This judge link doesn’t work">
          <Card>
            <p className="text-ink-soft">The organiser may have made a new judge link, or this one was copied wrong. Ask the organiser to send it again.</p>
            <Actions>
              <LinkButton primary href={acct.signedIn ? "/events" : "/"}>
                {acct.signedIn ? "Go to your events" : "Go to Simple Hack"}
              </LinkButton>
            </Actions>
          </Card>
        </Page>
      );
    return <Problem text={isOffline(q.error) ? OFFLINE_LOAD : errText(q.error)} />;
  }
  const info = q.data;
  if (info.joinable === false)
    return (
      <Page title={closedLine(info.stage, "judge").replace(/\.$/, "")} lines={[info.title]}>
        <Actions>
          <LinkButton href={eventPage(info.slug)}>See the event page</LinkButton>
        </Actions>
      </Page>
    );
  if (member && mine.data) return <Already info={info} role={mine.data.role} />;
  const meta = [info.organiser_name ? info.organiser_name + (info.organisation ? " · " + info.organisation : "") : "", judgeDates(info)].filter(Boolean).join(" · ");
  return (
    <Page title="You’re invited to judge" lines={[info.title, meta, info.tagline, <a key="p" href={eventPage(info.slug)}>See the event page</a>]}>
      <Coc text={info.coc_default} extra={info.coc_text} />
      {acct.signedIn ? <Accept code={code} info={info} name={accountName(info.account_name, acct.me)} /> : <SignInStep><p>Read the invitation, then sign in to accept.</p></SignInStep>}
    </Page>
  );
}
