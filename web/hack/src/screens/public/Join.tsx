// Join link (/join/{code}), signed in or out: the event and what joining asks
// (questions, code of conduct) before sign-in; the same questions as the form
// after; "already in" (or waiting, or not accepted) for a member; why not for
// a closed event or a dead link. Joining is a full page load into the event,
// so the frame reads the new membership (public-join.js).
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Button } from "@kit/components/ui/button";
import { Card } from "@kit/components/ui/card";
import { Check } from "@kit/components/ui/choice";
import { Field, FieldMessage, Textarea } from "@kit/components/ui/input";
import { Notice } from "@kit/components/ui/notice";
import type { ApiError } from "@kit/api/call";
import { useHack } from "@/lib/client";
import type { ScreenProps } from "@/routes/registry";
import { JOIN_ERRORS, OFFLINE_LOAD, accountName, checkJoin, errText, isGone, isOffline, joinBody, memberLine, unanswered, closedLine, type JoinInfo } from "./data";
import { Actions, Coc, LinkButton, LinkLoading, NameLine, Page, Problem, SignInStep, eventPage, useAccount, useLink } from "./parts";

const team = (slug: string) => eventPage(slug) + "/team";

function Already({ info, title, approval }: { info: JoinInfo; title: string; approval?: string }) {
  const line = memberLine(approval, title);
  return (
    <Page title={"Join " + title} lines={[organiser(info)]}>
      <Notice tone={line.tone} title={line.text} />
      <Actions>{line.open === "team" ? <LinkButton primary href={team(info.slug)}>Go to the event</LinkButton> : <LinkButton href={eventPage(info.slug)}>Open the event</LinkButton>}</Actions>
    </Page>
  );
}

const organiser = (info: JoinInfo) => (info.organiser_name ? info.organiser_name + (info.organisation ? " · " + info.organisation : "") : "");

function Form({ code, info, title, name }: { code: string; info: JoinInfo; title: string; name: string }) {
  const api = useHack();
  const questions = info.signup_questions || [];
  const [answers, setAnswers] = useState<Record<string, string>>({});
  const [typed, setTyped] = useState("");
  const [coc, setCoc] = useState(false);
  const [errs, setErrs] = useState<{ coc?: string; name?: string; form?: string; q?: Record<string, string> }>({});
  const [busy, setBusy] = useState(false);
  const [already, setAlready] = useState(false);
  if (already) return <Already info={info} title={title} />;

  const send = async (e: React.FormEvent) => {
    e.preventDefault();
    const missing = unanswered(questions, answers);
    const found = { ...checkJoin({ coc, askName: !name, name: typed }), q: missing };
    setErrs(found);
    if (found.coc || found.name || Object.keys(missing).length) return;
    setBusy(true);
    try {
      const r = (await api.invites.acceptJoin(code, joinBody({ askName: !name, name: typed, answers }))) as { event?: { slug?: string } };
      location.assign(team(r.event?.slug || info.slug));
    } catch (err) {
      setBusy(false);
      if ((err as ApiError).code === "already_member") setAlready(true);
      else if ((err as ApiError).status !== 401) setErrs({ form: errText(err, JOIN_ERRORS) });
    }
  };

  return (
    <Page title={"Join " + title} lines={[organiser(info), <a key="p" href={eventPage(info.slug)}>See the event page</a>]}>
      <Card>
        <form noValidate onSubmit={send}>
          <h2 className="mb-4 type-section">{info.approval_required ? "Apply" : "Your details"}</h2>
          <NameLine name={name} id="join-name" value={typed} onChange={setTyped} error={errs.name} />
          {questions.map((q, i) => (
            <Field key={q.id} id={"join-q-" + i} label={q.prompt + (q.required ? "" : " (optional)")} error={errs.q?.[q.id]}>
              <Textarea id={"join-q-" + i} required={q.required} aria-invalid={!!errs.q?.[q.id]} value={answers[q.id] || ""} onChange={(e) => setAnswers({ ...answers, [q.id]: e.target.value })} />
            </Field>
          ))}
          <Coc text={info.coc_default} extra={info.coc_text} />
          <div className="mt-3">
            <Check checked={coc} onChange={setCoc}>
              I agree to the code of conduct
            </Check>
            {errs.coc && <FieldMessage tone="bad">{errs.coc}</FieldMessage>}
          </div>
          {errs.form && <FieldMessage tone="bad">{errs.form}</FieldMessage>}
          <Button type="submit" variant="primary" size="lg" className="mt-5 md:w-auto md:px-8" disabled={busy}>
            {info.approval_required ? "Send application" : "Join event"}
          </Button>
        </form>
      </Card>
    </Page>
  );
}

export default function Join({ match }: ScreenProps) {
  const code = match.code || "";
  const api = useHack();
  const acct = useAccount();
  const q = useLink(["join", code], () => api.invites.join(code) as Promise<JoinInfo>);
  const slug = q.data?.slug;
  const member = !!slug && !!acct.events?.some((e) => e.slug === slug);
  const mine = useQuery({ queryKey: ["event", slug], queryFn: () => api.event.get(slug!), enabled: member, retry: false });

  if (q.isPending || !acct.ready || (member && mine.isPending)) return <LinkLoading label="Loading the invitation" />;
  if (q.isError) {
    if (isGone(q.error, "invalid_code")) return <Problem text={JOIN_ERRORS.invalid_code} />;
    return <Problem text={isOffline(q.error) ? OFFLINE_LOAD : errText(q.error, JOIN_ERRORS)} />;
  }
  const info = q.data;
  const title = info.title || "this event";
  if (member && mine.data && !mine.data.admin_view) return <Already info={info} title={title} approval={mine.data.me?.approval_status} />;
  if (info.joinable === false)
    return (
      <Page title={"Join " + title} lines={[organiser(info)]}>
        <Notice title={closedLine(info.stage, "join")} />
        <Actions>
          <LinkButton href={eventPage(info.slug)}>Open the event</LinkButton>
        </Actions>
      </Page>
    );
  if (!acct.signedIn) {
    const questions = info.signup_questions || [];
    return (
      <Page title={"Join " + title} lines={[organiser(info), <a key="p" href={eventPage(info.slug)}>See the event page</a>]}>
        <SignInStep title="What you'll be asked" label="Sign in to join">
          <ul className="m-0 list-disc pl-5">
            {questions.length ? (
              questions.map((x) => <li key={x.id}>{x.prompt + (x.required ? "" : " (optional)")}</li>)
            ) : (
              <li>No questions. Just agree to the code of conduct.</li>
            )}
          </ul>
          <Coc text={info.coc_default} extra={info.coc_text} />
        </SignInStep>
      </Page>
    );
  }
  return <Form code={code} info={info} title={title} name={accountName(info.account_name, acct.me)} />;
}
