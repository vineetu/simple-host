// Co-organiser invite (/organiser/{code}), signed in or out: what accepting
// gives (everything the organiser can do, deleting the event included), the
// code of conduct, then sign in and accept. A dead or closed invite says so.
// Accepting opens the event's organiser area (public-organiser-invite.js).
import { useState } from "react";
import { Button } from "@kit/components/ui/button";
import { Card } from "@kit/components/ui/card";
import { Check } from "@kit/components/ui/choice";
import { FieldMessage } from "@kit/components/ui/input";
import { Notice } from "@kit/components/ui/notice";
import type { ApiError } from "@kit/api/call";
import { useHack } from "@/lib/client";
import { href } from "@/routes/resolve";
import type { ScreenProps } from "@/routes/registry";
import { accountName, checkJoin, errText, joinBody, type OrganiserInvite as Invite } from "./data";
import { Actions, Coc, LinkButton, LinkLoading, NameLine, Page, SignInStep, useAccount, useLink } from "./parts";

const NOT_OPEN = "This invitation isn’t open any more. Ask the organiser for a new one.";

function Accept({ code, name, login }: { code: string; name: string; login: string }) {
  const api = useHack();
  const [typed, setTyped] = useState("");
  const [coc, setCoc] = useState(false);
  const [errs, setErrs] = useState<{ coc?: string; name?: string; form?: string }>({});
  const [busy, setBusy] = useState(false);
  const [already, setAlready] = useState(false);
  if (already)
    return (
      <div className="mt-6">
        <Notice title="You already belong to this event." />
        <Actions>
          <LinkButton primary href="/events">
            Home
          </LinkButton>
        </Actions>
      </div>
    );
  const send = async (e: React.FormEvent) => {
    e.preventDefault();
    const found = checkJoin({ coc, askName: !name, name: typed });
    if (found.coc) found.coc = "You must accept the code of conduct.";
    setErrs(found);
    if (found.coc || found.name) return;
    setBusy(true);
    try {
      const r = (await api.invites.acceptOrganiser(code, joinBody({ askName: !name, name: typed }))) as { event?: { slug?: string } };
      const slug = r.event?.slug;
      location.assign(slug ? href("organiser", slug) : "/events");
    } catch (err) {
      setBusy(false);
      if ((err as ApiError).code === "already_member") setAlready(true);
      else if ((err as ApiError).status !== 401) setErrs({ form: errText(err) });
    }
  };
  return (
    <Card className="mt-6">
      <form noValidate onSubmit={send}>
        <h2 className="type-section">Accept invitation</h2>
        <p className="mb-4 text-sm text-ink-soft">Signed in as {login || "you"}</p>
        <NameLine name={name} id="accept-name" value={typed} onChange={setTyped} error={errs.name} />
        <Check checked={coc} onChange={setCoc}>
          I agree to the code of conduct
        </Check>
        {errs.coc && <FieldMessage tone="bad">{errs.coc}</FieldMessage>}
        <section aria-label="Full organiser rights" className="mt-5 rounded-field border-[1.5px] border-bad px-4 py-3">
          <h3 className="type-section text-bad">Full organiser rights</h3>
          <p className="mt-1 text-ink-soft">You’ll have full control of this event, including Delete this event. Accept?</p>
          {errs.form && <FieldMessage tone="bad">{errs.form}</FieldMessage>}
          <Button type="submit" variant="danger" className="mt-3" disabled={busy}>
            Accept and co-organise
          </Button>
        </section>
      </form>
    </Card>
  );
}

export default function OrganiserInvite({ match }: ScreenProps) {
  const code = match.code || "";
  const api = useHack();
  const acct = useAccount();
  const q = useLink(["organiser", code], () => api.invites.organiser(code) as Promise<Invite>);

  if (q.isPending || !acct.ready) return <LinkLoading label="Loading the invitation" />;
  if (q.isError) {
    const err = q.error as ApiError;
    const text = err.status === 0 ? "Cannot reach the server." : err.status === 410 ? NOT_OPEN : errText(err, {}, NOT_OPEN);
    return (
      <Page title="Co-organise this event">
        <Notice tone="bad" title={text} />
      </Page>
    );
  }
  const invite = q.data;
  if (!invite.joinable)
    return (
      <Page title={invite.title}>
        <Notice tone="bad" title={NOT_OPEN} />
      </Page>
    );
  const who = invite.organiser_name;
  return (
    <Page title="You’re invited to co-organise" lines={[invite.title]}>
      <p>
        Accepting gives you everything {who || "the organiser"} can do on this event, including editing, publishing results, and deleting it. Only {who || "the creator"}, as the
        creator, can’t be removed.
      </p>
      <Coc text={invite.coc_default} extra={invite.coc_text} />
      {acct.signedIn ? (
        <Accept code={code} name={accountName(invite.account_name, acct.me)} login={acct.me?.login || acct.me?.username || acct.me?.email || ""} />
      ) : (
        <SignInStep>
          <p>Read the invitation, then sign in to accept.</p>
        </SignInStep>
      )}
    </Page>
  );
}
