// Apply (/e/{slug}/apply), signed in or out: the event's header, then what
// applying asks (signed out: nothing the organisers wrote is shown before
// sign-in), the form (name when the account has none, where you're based,
// age range, gender, the organisers' questions, the code of conduct), or the
// person's own state (waiting, in, not accepted, another role), or why the
// event takes no applications. Sending reloads the page, which then shows
// the server's answer (public-apply.js).
import { useState } from "react";
import { Button } from "@kit/components/ui/button";
import { Card } from "@kit/components/ui/card";
import { Check, RadioCards } from "@kit/components/ui/choice";
import { Dialog, DialogActions } from "@kit/components/ui/dialog";
import { Field, FieldMessage, Input, NativeSelect, Textarea } from "@kit/components/ui/input";
import { Notice } from "@kit/components/ui/notice";
import type { ApiError } from "@kit/api/call";
import { useHack } from "@/lib/client";
import { forget } from "@/lib/session";
import type { ScreenProps } from "@/routes/registry";
import {
  AGE_RANGES,
  APPLY_ERRORS,
  GENDERS,
  OFFLINE_LOAD,
  accountName,
  applyDates,
  applyField,
  article,
  countries,
  errText,
  isGone,
  isOffline,
  unanswered,
  zoneAbbr,
  type ApplyInfo,
} from "./data";
import { Actions, Coc, LinkButton, LinkLoading, Page, Problem, SignInStep, eventPage, useAccount, useLink, NameLine } from "./parts";

type Errs = Record<string, string>;

function Form({ slug, info, name, email }: { slug: string; info: ApplyInfo; name: string; email: string }) {
  const api = useHack();
  const title = info.title || "this event";
  const questions = info.signup_questions || [];
  const ages = info.fields?.age_range?.length ? info.fields.age_range : AGE_RANGES;
  const genders = info.fields?.gender?.length ? info.fields.gender : GENDERS;
  const [v, setV] = useState({ name: "", city: "", country: "", age: "", gender: "" });
  const [answers, setAnswers] = useState<Record<string, string>>({});
  const [coc, setCoc] = useState(false);
  const [errs, setErrs] = useState<Errs>({});
  const [qErrs, setQErrs] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const [warnOut, setWarnOut] = useState(false);
  const set = (k: keyof typeof v) => (x: string) => setV({ ...v, [k]: x });

  const signOut = async () => {
    let failed = false;
    try {
      await api.auth.signOut();
    } catch (e) {
      const s = (e as ApiError).status;
      failed = s !== 400 && s !== 401;
    }
    forget();
    if (failed) setWarnOut(true);
    else location.replace("/");
  };

  const send = async (e: React.FormEvent) => {
    e.preventDefault();
    const found: Errs = {};
    if (!coc) found.coc = APPLY_ERRORS.coc_required;
    if (!name && !v.name.trim()) found.name = "Enter your name.";
    const missing = unanswered(questions, answers);
    setErrs(found);
    setQErrs(missing);
    if (Object.keys(found).length || Object.keys(missing).length) return;
    const body: Record<string, unknown> = { accept_coc: true, city: v.city.trim(), country: v.country, age_range: v.age, gender: v.gender, answers };
    if (!name) body.display_name = v.name.trim();
    setBusy(true);
    try {
      await api.people.apply(slug, body);
      location.assign(location.pathname);
    } catch (err) {
      setBusy(false);
      if ((err as ApiError).status === 401) return;
      const field = applyField((err as ApiError).code) || "form";
      setErrs({ [field]: errText(err, APPLY_ERRORS) });
    }
  };

  return (
    <Card className="max-w-[720px]">
      <form noValidate onSubmit={send}>
        <h2 className="type-section">Your application</h2>
        <p className="mb-4 flex flex-wrap items-center gap-x-3 text-sm text-ink-soft">
          Signed in as {email}
          <Button variant="link" onClick={signOut} className="min-h-11">
            Not you? Sign out
          </Button>
        </p>
        <NameLine name={name} id="ap-name" value={v.name} onChange={set("name")} error={errs.name} />

        <fieldset className="mt-2 mb-4 border-0 p-0">
          <legend className="mb-2 type-section text-base">Where you're based (optional)</legend>
          <div className="grid gap-x-4 md:grid-cols-2">
            <Field id="ap-city" label="City" error={errs.city}>
              <Input id="ap-city" maxLength={80} autoComplete="address-level2" value={v.city} aria-invalid={!!errs.city} onChange={(e) => set("city")(e.target.value)} />
            </Field>
            <Field id="ap-country" label="Country" error={errs.country}>
              <NativeSelect id="ap-country" className="h-12 w-full" autoComplete="country" value={v.country} aria-invalid={!!errs.country} onChange={(e) => set("country")(e.target.value)}>
                <option value="">Choose a country</option>
                {countries().map((c) => (
                  <option key={c.code} value={c.code}>
                    {c.name}
                  </option>
                ))}
              </NativeSelect>
            </Field>
          </div>
        </fieldset>

        <div className="mb-4">
          <p className="mb-2 type-section text-base" aria-hidden="true">
            Age range (optional)
          </p>
          <RadioCards
            label="Age range (optional)"
            value={v.age}
            onChange={set("age")}
            options={ages.map((o) => ({
              value: o.value,
              title: o.label,
              hint: "",
              more:
                o.value === "under_18" ? (
                  <p className="text-sm">
                    If you're under 18, a parent or guardian needs to agree to Simple Hack's <a href="/terms">Terms</a> for you.
                  </p>
                ) : undefined,
            }))}
          />
          {errs.age && <FieldMessage tone="bad">{errs.age}</FieldMessage>}
        </div>

        <Field id="ap-gender" label="Gender (optional)" error={errs.gender}>
          <NativeSelect id="ap-gender" className="h-12 w-full" value={v.gender} aria-invalid={!!errs.gender} onChange={(e) => set("gender")(e.target.value)}>
            <option value="">Choose</option>
            {genders.map((o) => (
              <option key={o.value} value={o.value}>
                {o.label}
              </option>
            ))}
          </NativeSelect>
        </Field>

        {questions.length > 0 && (
          <div className="mt-6">
            <h3 className="mb-3 type-section">Questions from the organisers</h3>
            {questions.map((q, i) => (
              <Field key={q.id} id={"ap-q-" + i} label={q.prompt + (q.required ? "" : " (optional)")} error={qErrs[q.id]}>
                <Textarea
                  id={"ap-q-" + i}
                  maxLength={500}
                  required={q.required}
                  aria-invalid={!!qErrs[q.id]}
                  value={answers[q.id] || ""}
                  onChange={(e) => setAnswers({ ...answers, [q.id]: e.target.value })}
                />
              </Field>
            ))}
          </div>
        )}

        <Coc text={info.coc_default} extra={info.coc_text} />
        <div className="mt-3">
          <Check checked={coc} onChange={setCoc}>
            I agree to the code of conduct
          </Check>
          {errs.coc && <FieldMessage tone="bad">{errs.coc}</FieldMessage>}
        </div>
        <p className="mt-4 text-sm text-ink-soft">Only the organisers of {title} see your answers. They're never shown on the event page or to other participants.</p>
        {errs.form && <FieldMessage tone="bad">{errs.form}</FieldMessage>}
        <Button type="submit" variant="primary" size="lg" className="mt-3" disabled={busy}>
          Send application
        </Button>
      </form>
      <Dialog open={warnOut} onOpenChange={(o) => !o && location.replace("/")} title="Signed out of this browser, but the server could not be reached to end its key.">
        <p className="text-ink-soft">If this is a shared computer, sign in again and choose “Sign out everywhere”.</p>
        <DialogActions>
          <Button variant="primary" onClick={() => location.replace("/")}>
            Done
          </Button>
        </DialogActions>
      </Dialog>
    </Card>
  );
}

export default function Apply({ match }: ScreenProps) {
  const slug = match.slug || "";
  const api = useHack();
  const acct = useAccount();
  const q = useLink(["apply", slug], () => api.people.applyForm(slug) as Promise<ApplyInfo>);
  const open = eventPage(slug);

  if (q.isPending || !acct.ready) return <LinkLoading label="Loading the event" />;
  if (q.isError) {
    if (isGone(q.error, "event_not_found"))
      return (
        <Page title="Apply">
          <Notice tone="bad" title="We couldn't find this event." />
          <Actions>
            <LinkButton primary href="/directory">
              Find a hackathon
            </LinkButton>
          </Actions>
        </Page>
      );
    return <Problem text={isOffline(q.error) ? OFFLINE_LOAD : errText(q.error, APPLY_ERRORS)} />;
  }
  const info = q.data;
  const title = info.title || "this event";
  const bits: string[] = [];
  if (info.organiser_name) bits.push("Organised by " + info.organiser_name + (info.organisation ? " (" + info.organisation + ")" : ""));
  const range = applyDates(info.starts_at, info.ends_at, info.time_zone);
  if (range) bits.push(range);
  const zone = zoneAbbr(info.time_zone);
  if (zone) bits.push("times in " + zone);
  const page = (body: React.ReactNode) => (
    <Page title={"Apply to " + title} lines={[bits.join(" · "), <a key="p" href={open}>See the event page</a>]}>
      {body}
    </Page>
  );

  const me = info.me;
  if (me) {
    if (me.role !== "participant")
      return page(
        <>
          <Notice title={`You're already part of this event as ${article(me.role || "")} ${me.role}.`} />
          <Actions>
            <LinkButton href="/events">Open the event</LinkButton>
          </Actions>
        </>,
      );
    if (me.approval_status === "approved")
      return page(
        <>
          <Notice title={`You're in ${title}.`} />
          <Actions>
            <LinkButton primary href={open + "/team"}>
              Go to the event
            </LinkButton>
          </Actions>
        </>,
      );
    if (me.approval_status === "rejected")
      return page(
        <>
          <Notice tone="bad" title="Your application wasn't accepted this time.">
            Organisers decide who joins each event.
          </Notice>
          <Actions>
            <LinkButton primary href="/directory">
              Find another hackathon
            </LinkButton>
            <LinkButton href={open}>See the event page</LinkButton>
          </Actions>
        </>,
      );
    return page(
      <>
        <Notice title="Your application is waiting for the organisers.">
          They decide who joins {title}. You'll see their answer here and on your Home. Once you're in, you can find a team.
        </Notice>
        <Actions>
          <LinkButton primary href="/events">
            Go to your Home
          </LinkButton>
          <LinkButton href={open}>See the event page</LinkButton>
        </Actions>
      </>,
    );
  }

  if (!info.applications_open) {
    if (info.closed_reason === "applications_off" || info.closed_reason === "not_listed")
      return page(
        <>
          <Notice title="This event isn't taking applications.">The organisers only take people with their join link. If you have one, open it to join.</Notice>
          <Actions>
            <LinkButton href={open}>See the event page</LinkButton>
            <LinkButton href="/directory">Find another hackathon</LinkButton>
          </Actions>
        </>,
      );
    return page(
      <>
        <Notice title="Sign-up has closed for this event." />
        <Actions>
          <LinkButton href={open}>See the event page</LinkButton>
        </Actions>
      </>,
    );
  }

  // A stale key: GET apply never refuses one, it just leaves "me" out; that is signed out.
  if (!acct.signedIn || !("me" in info))
    return page(
      <SignInStep
        title="Apply to join"
        label="Sign in to apply"
        after={<p className="mt-3 text-sm text-ink-soft">Have a join link from the organisers? Open that instead.</p>}
      >
        <p>Sign in with your email, then fill in one short form:</p>
        <ul className="my-3 list-disc pl-5">
          <li>
            Your name, if your account has none yet
            <span className="block text-sm text-ink-soft">Saved to your account. Shown to the organisers, and to your teammates once you're in.</span>
          </li>
          <li>
            Where you're based, age range and gender
            <span className="block text-sm text-ink-soft">All three are optional.</span>
          </li>
          <li>
            A few questions from the organisers
            <span className="block text-sm text-ink-soft">And their code of conduct to agree to.</span>
          </li>
        </ul>
        <p>The organisers decide who joins. You'll see their answer here and on your Simple Hack Home.</p>
      </SignInStep>,
    );

  const email = acct.me?.login || acct.me?.email || acct.me?.username || "";
  return page(<Form slug={slug} info={info} name={accountName(info.account_name, acct.me)} email={email} />);
}
