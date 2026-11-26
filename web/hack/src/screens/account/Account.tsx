// Account (/account): the person's one name, sign-in email and password,
// linked Google sign-in, connected AI apps, email preferences, and the
// actions that cannot be undone (a new key, sign out everywhere, delete the
// account), each in a red-edged part at the bottom of its tab. The same for
// every role. The open tab is the address's #hash.
import { useState, type ReactNode } from "react";
import { useLocation, useNavigate } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Button } from "@kit/components/ui/button";
import { Check } from "@kit/components/ui/choice";
import { CopyButton } from "@kit/components/ui/copy";
import { Dialog, DialogActions } from "@kit/components/ui/dialog";
import { EmptyState } from "@kit/components/ui/empty-state";
import { Field, FieldMessage, Input } from "@kit/components/ui/input";
import { PageTitle, Section } from "@kit/components/ui/page-shell";
import { Skeleton } from "@kit/components/ui/skeleton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@kit/components/ui/tabs";
import { useToast } from "@kit/components/ui/toast";
import { LeaveGuard, useDirty } from "./leave-guard";
import { paths, useHack } from "@/lib/client";
import type { Account as Me } from "@/lib/client/me";
import { dateTime } from "@/lib/time";
import { forget } from "@/lib/session";
import { useMe } from "@/app/data";
import { Failed } from "@/app/states";
import { useAsk, type Ask } from "./Ask";
import { ConnectApps } from "./ConnectApps";
import { accountErr, canOpen, identityLine, linkGoogle, providerName, words, type Words } from "./data";

const TABS = [
  ["profile", "Profile"],
  ["sign-in", "Sign-in"],
  ["apps", "AI apps"],
  ["emails", "Emails"],
] as const;

/** Danger is a red-edged part at the bottom of a tab: rows of one action each. */
function Danger({ title, children }: { title: string; children: ReactNode }) {
  return (
    <Section title={title} className="mt-10 rounded-field border-[1.5px] border-bad px-4 py-3">
      <div className="divide-y divide-rule">{children}</div>
    </Section>
  );
}

function DangerRow({ title, text, children }: { title: string; text: string; children: ReactNode }) {
  return (
    <div className="flex flex-wrap items-center gap-3 py-3">
      <div className="min-w-0 flex-[1_1_16rem]">
        <p className="font-semibold">{title}</p>
        <p className="text-sm text-ink-soft">{text}</p>
      </div>
      {children}
    </div>
  );
}

function useSetMe() {
  const qc = useQueryClient();
  return (patch: Partial<Me>) => qc.setQueryData<Me>(["me"], (m) => ({ ...(m || {}), ...patch }));
}

// ---- Profile ----

function NamePart({ me, w }: { me: Me; w: Words }) {
  const api = useHack();
  const toast = useToast();
  const setMe = useSetMe();
  const [name, setName] = useState(me.display_name || "");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  useDirty(name.trim() !== (me.display_name || "").trim());
  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    setErr("");
    setBusy(true);
    const sent = name.trim();
    try {
      const r = await api.me.update({ display_name: sent });
      const saved = typeof r?.display_name === "string" ? r.display_name : sent;
      setMe({ display_name: saved });
      setName((now) => (now.trim() === sent ? saved : now));
      toast("Saved");
    } catch (e) {
      if ((e as { status?: number }).status !== 401) setErr(accountErr(e, w, "Could not save"));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Section title="Your name">
      <form onSubmit={save} noValidate className="max-w-prose">
        <Field
          id="acct-name"
          label={<span className="sr-only">Name</span>}
          error={err}
          hint="The one name teams, organisers and judges see in every event you join. A change here updates it everywhere at once."
        >
          <div className="flex gap-2">
            <Input id="acct-name" maxLength={100} value={name} onChange={(e) => setName(e.target.value)} aria-describedby="acct-name-msg" aria-invalid={!!err} />
            <Button type="submit" variant="primary" disabled={busy}>
              Save
            </Button>
          </div>
        </Field>
      </form>
    </Section>
  );
}

function EmailPart({ w, onChanged }: { w: Words; onChanged: (next: string) => void }) {
  const api = useHack();
  const toast = useToast();
  const [step, setStep] = useState<"row" | "email" | "code">("row");
  const [email, setEmail] = useState("");
  const [to, setTo] = useState({ next: "", current: "" });
  const [code, setCode] = useState("");
  const [current, setCurrent] = useState("");
  const [errs, setErrs] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  useDirty((step === "email" && !!(email || current)) || (step === "code" && !!code));
  const show = (s: typeof step) => {
    setStep(s);
    setErrs({});
    setEmail("");
    setCode("");
    setCurrent("");
  };
  const failed = (e: unknown, field: string, fallback: string) => {
    if ((e as { status?: number }).status !== 401) setErrs({ [field]: accountErr(e, w, fallback) });
  };

  if (!w.realEmail)
    return (
      <Section title="Email">
        <p className="text-ink-soft">Your account has no email address.</p>
      </Section>
    );
  if (step === "email")
    return (
      <Section title="Email">
        <form
          noValidate
          className="max-w-prose"
          onSubmit={async (e) => {
            e.preventDefault();
            const v = email.trim();
            if (!v) return setErrs({ email: "Enter the new email." });
            setBusy(true);
            try {
              const r = await api.me.requestEmailChange(v);
              setTo({ next: r?.email || v, current: r?.current_email || w.email });
              show("code");
            } catch (e) {
              failed(e, "email", "Could not send a code");
            } finally {
              setBusy(false);
            }
          }}
        >
          <Field id="acct-email-input" label="New email" error={errs.email}>
            <Input id="acct-email-input" type="email" autoComplete="email" autoFocus value={email} onChange={(e) => setEmail(e.target.value)} aria-invalid={!!errs.email} aria-describedby="acct-email-input-msg" />
          </Field>
          <div className="flex flex-wrap gap-2.5">
            <Button type="submit" disabled={busy}>
              Send codes
            </Button>
            <Button onClick={() => show("row")}>Cancel</Button>
          </div>
        </form>
      </Section>
    );
  if (step === "code")
    return (
      <Section title="Email">
        <form
          noValidate
          className="max-w-prose"
          onSubmit={async (e) => {
            e.preventDefault();
            const bad: Record<string, string> = {};
            if (!/^\d{6}$/.test(code.trim())) bad.code = "Enter the 6-digit code from the new address.";
            if (!/^\d{6}$/.test(current.trim())) bad.current = "Enter the 6-digit code from your current address.";
            if (Object.keys(bad).length) return setErrs(bad);
            setBusy(true);
            try {
              const r = await api.me.confirmEmailChange(code.trim(), current.trim());
              const next = r?.email || r?.username || "";
              if (next) onChanged(next);
              show("row");
              toast("Your sign-in email is now " + (next || "the new address") + ". Your other keys were signed out.");
            } catch (e) {
              failed(e, "form", "Could not change your email");
            } finally {
              setBusy(false);
            }
          }}
        >
          <p className="mb-3">
            Enter the codes sent to <span className="text-ink">{to.next}</span> and <span className="text-ink">{to.current}</span>.
          </p>
          <Field id="acct-code" label="New email code" error={errs.code}>
            <Input id="acct-code" inputMode="numeric" autoComplete="one-time-code" autoFocus value={code} onChange={(e) => setCode(e.target.value)} aria-invalid={!!errs.code} aria-describedby="acct-code-msg" />
          </Field>
          <Field id="acct-current-code" label="Current email code" error={errs.current}>
            <Input id="acct-current-code" inputMode="numeric" value={current} onChange={(e) => setCurrent(e.target.value)} aria-invalid={!!errs.current} aria-describedby="acct-current-code-msg" />
          </Field>
          {errs.form && <FieldMessage tone="bad">{errs.form}</FieldMessage>}
          <div className="mt-4 flex flex-wrap gap-2.5">
            <Button type="submit" disabled={busy}>
              Change email
            </Button>
            <Button onClick={() => show("row")}>Cancel</Button>
          </div>
        </form>
      </Section>
    );
  return (
    <Section title="Email">
      <p>
        You sign in with <span className="text-ink">{w.realEmail}</span>. A change needs a code from this address and one from the new one.
      </p>
      <Button className="mt-3" onClick={() => show("email")}>
        Change email
      </Button>
    </Section>
  );
}

function PasswordPart({ w }: { w: Words }) {
  const api = useHack();
  const toast = useToast();
  const [f, setF] = useState({ current: "", next: "", confirm: "" });
  const [errs, setErrs] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  useDirty(!!(f.current || f.next || f.confirm));
  const set = (k: keyof typeof f) => (e: React.ChangeEvent<HTMLInputElement>) => setF({ ...f, [k]: e.target.value });
  return (
    <Section title="Password">
      {w.login && <p className="mb-3 text-sm text-ink-soft">You sign in with {w.login}.</p>}
      <form
        noValidate
        className="max-w-prose"
        onSubmit={async (e) => {
          e.preventDefault();
          const bad: Record<string, string> = {};
          if (!f.current) bad.current = "Enter your current password.";
          if (f.next.length < 8) bad.next = "New password must be at least 8 characters.";
          else if (f.next !== f.confirm) bad.confirm = "New passwords do not match.";
          setErrs(bad);
          if (Object.keys(bad).length) return;
          setBusy(true);
          try {
            await api.me.setPassword({ current_password: f.current, new_password: f.next });
            setF({ current: "", next: "", confirm: "" });
            toast("Password changed");
          } catch (e) {
            if ((e as { status?: number }).status !== 401) setErrs({ form: accountErr(e, w, "Could not change password") });
          } finally {
            setBusy(false);
          }
        }}
      >
        <Field id="acct-password-current" label="Current password" error={errs.current}>
          <Input id="acct-password-current" type="password" autoComplete="current-password" value={f.current} onChange={set("current")} aria-invalid={!!errs.current} aria-describedby="acct-password-current-msg" />
        </Field>
        <Field id="acct-password-new" label="New password" error={errs.next} hint="At least 8 characters. Your other keys and sessions are not affected.">
          <Input id="acct-password-new" type="password" autoComplete="new-password" value={f.next} onChange={set("next")} aria-invalid={!!errs.next} aria-describedby="acct-password-new-msg" />
        </Field>
        <Field id="acct-password-confirm" label="Confirm new password" error={errs.confirm}>
          <Input id="acct-password-confirm" type="password" autoComplete="new-password" value={f.confirm} onChange={set("confirm")} aria-invalid={!!errs.confirm} aria-describedby="acct-password-confirm-msg" />
        </Field>
        {errs.form && <FieldMessage tone="bad">{errs.form}</FieldMessage>}
        <Button type="submit" className="mt-2" disabled={busy}>
          Change password
        </Button>
      </form>
    </Section>
  );
}

function DeleteAccount({ w }: { w: Words }) {
  const api = useHack();
  const [open, setOpen] = useState(false);
  const [typed, setTyped] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const [gone, setGone] = useState(false);
  const want = w.email.toLowerCase();
  const matches = !!want && typed.trim().toLowerCase() === want;
  const leave = () => {
    forget();
    location.replace("/");
  };
  return (
    <>
      <DangerRow title="Delete your account" text="Delete your account and all its data at once and for good. Blocked while you’re organising an event that hasn’t ended.">
        <Button
          variant="danger"
          onClick={() => {
            setTyped("");
            setErr("");
            setOpen(true);
          }}
        >
          Delete account
        </Button>
      </DangerRow>
      <Dialog open={open} onOpenChange={(o) => !busy && setOpen(o)} title="Delete your account?" description="Your account and all its data go at once and for good.">
        <form
          noValidate
          onSubmit={async (e) => {
            e.preventDefault();
            if (!matches) return;
            setBusy(true);
            setErr("");
            try {
              await api.me.remove({ confirm: w.confirm || typed.trim() });
              setOpen(false);
              setGone(true);
            } catch (e) {
              if ((e as { status?: number }).status !== 401) setErr(accountErr(e, w, "Could not delete your account"));
            } finally {
              setBusy(false);
            }
          }}
        >
          <Field id="acct-delete-input" label={`Type ${w.email} to confirm. This cannot be undone.`} error={err}>
            <Input id="acct-delete-input" type="email" autoComplete="off" autoFocus value={typed} onChange={(e) => setTyped(e.target.value)} aria-describedby="acct-delete-input-msg" />
          </Field>
          <DialogActions>
            <Button onClick={() => setOpen(false)}>Cancel</Button>
            <Button type="submit" variant="danger" disabled={busy || !matches}>
              Delete account
            </Button>
          </DialogActions>
        </form>
      </Dialog>
      <Dialog open={gone} onOpenChange={(o) => !o && leave()} title="Your account and all its data were deleted." description="A confirmation is on its way to your email.">
        <DialogActions>
          <Button variant="primary" onClick={leave}>
            Done
          </Button>
        </DialogActions>
      </Dialog>
    </>
  );
}

// ---- Sign-in ----

function GooglePart({ w, ask }: { w: Words; ask: Ask }) {
  const api = useHack();
  const toast = useToast();
  const qc = useQueryClient();
  const ids = useQuery({ queryKey: ["identities"], queryFn: () => api.me.identityList() });
  const prov = useQuery({ queryKey: ["providers"], queryFn: () => api.auth.providers().catch(() => null), staleTime: Infinity });
  const oidcLabel = prov.data?.oidc_label || "";
  const googleOn = (prov.data?.providers || []).includes("google");
  const linked = (ids.data || []).some((i) => i.provider === "google");
  const canLink = googleOn && !linked && !ids.isPending;
  return (
    <Section title="Google sign-in">
      {ids.isPending ? (
        <div role="status" aria-busy="true" aria-label="Loading linked sign-ins">
          <Skeleton className="h-12 w-full" />
        </div>
      ) : ids.isError ? (
        <div className="flex flex-wrap items-center gap-3">
          <FieldMessage tone="bad">{accountErr(ids.error, w, "Could not load linked sign-ins")}</FieldMessage>
          <Button onClick={() => ids.refetch()}>Try again</Button>
        </div>
      ) : !ids.data.length ? (
        <p className="text-ink-soft">No other sign-in is linked.</p>
      ) : (
        <ul aria-label="Linked sign-ins" className="m-0 list-none rounded-wobble border border-rule bg-card px-3 py-1">
          {ids.data.map((i, n) => {
            const who = providerName(i, oidcLabel);
            const label = who + " (" + (i.email || "no email") + ")";
            return (
              <li key={i.id || n} className="flex min-h-14 flex-wrap items-center gap-3 border-b border-rule px-1 py-2 last:border-b-0">
                <span className="min-w-0 flex-[1_1_14rem]">{identityLine(i, who, i.linked_at ? dateTime(i.linked_at, undefined, { dateOnly: true }) : "")}</span>
                {i.id && (
                  <Button
                    variant="danger"
                    onClick={() =>
                      ask({
                        title: "Unlink " + label + "?",
                        body: "It will no longer sign in to your account. You can still sign in with your email.",
                        confirmLabel: "Unlink",
                        danger: true,
                        run: async () => {
                          await api.me.removeIdentity(i.id!);
                          await qc.invalidateQueries({ queryKey: ["identities"] });
                        },
                        failed: "Could not unlink",
                        done: "Unlinked",
                      })
                    }
                  >
                    Unlink
                  </Button>
                )}
              </li>
            );
          })}
        </ul>
      )}
      {canLink && (
        <div className="mt-3">
          {w.realEmail && <p className="mb-2 text-sm text-ink-soft">Use the Google account with your sign-in email.</p>}
          <Button
            onClick={() =>
              ask({
                title: "Link Google?",
                body:
                  "Only continue with the Google account for " +
                  (w.email || "this email") +
                  ". Choosing a different Google account signs you into a different Simple Hack account instead.",
                confirmLabel: "Continue to Google",
                run: async () => {
                  if (!(await linkGoogle(paths.oauthStart))) toast("This browser is blocking storage sign-in needs. Use the emailed code.", { bad: true });
                },
              })
            }
          >
            Link Google
          </Button>
        </div>
      )}
    </Section>
  );
}

function KeyDialog({ apiKey, onClose }: { apiKey: string; onClose: () => void }) {
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()} title="Your new account key" description="This is the only key now. Copy it for your agent or CLI.">
      <div className="flex gap-2">
        <Input readOnly value={apiKey} aria-label="Your new account key" className="h-11 font-mono text-sm" autoFocus onFocus={(e) => e.target.select()} />
        <CopyButton text={apiKey} />
      </div>
      <DialogActions>
        <Button variant="primary" onClick={onClose}>
          Done
        </Button>
      </DialogActions>
    </Dialog>
  );
}

function KeyRows({ ask }: { ask: Ask }) {
  const api = useHack();
  const toast = useToast();
  const [shown, setShown] = useState("");
  // The browser keeps the new key, as the old screen did (localStorage "apiKey"); every later call signs with it.
  const rotate = async (done?: string) => {
    const r = await api.me.rotateKey();
    if (!r?.api_key) throw new Error(done ? "Could not sign out everywhere" : "Something went wrong");
    try {
      localStorage.setItem("apiKey", r.api_key);
    } catch {
      /* storage blocked: the key is still shown */
    }
    if (done) toast(done);
    setShown(r.api_key);
  };
  return (
    <>
      <DangerRow title="Make a new account key" text="The old key stops working at once, including in connected AI apps.">
        <Button
          variant="danger"
          onClick={() =>
            ask({ title: "Make a new account key?", body: "The old key stops working at once, including in connected AI apps.", confirmLabel: "New key", danger: true, run: () => rotate() })
          }
        >
          New key
        </Button>
      </DangerRow>
      <DangerRow title="Sign out everywhere" text="Every key stops working and connected apps are disconnected. This browser keeps the new key.">
        <Button
          variant="danger"
          onClick={() =>
            ask({
              title: "Sign out everywhere?",
              body: "Every key on this account stops working, and connected apps (ChatGPT, Claude, Grok) are disconnected, and every sign-in on your sites ends. This browser stays signed in. Your AI apps will need the new key.",
              confirmLabel: "Sign out everywhere",
              danger: true,
              run: () => rotate("Signed out everywhere. This browser has the new key."),
              failed: "Could not sign out everywhere",
            })
          }
        >
          Sign out everywhere
        </Button>
      </DangerRow>
      {shown && <KeyDialog apiKey={shown} onClose={() => setShown("")} />}
    </>
  );
}

// ---- Emails ----

function EmailsPart({ me, w }: { me: Me; w: Words }) {
  const api = useHack();
  const toast = useToast();
  const qc = useQueryClient();
  const setMe = useSetMe();
  const prefs = useQuery({ queryKey: ["preferences"], queryFn: () => api.hack.emailPreferences() });
  const alertsOn = !!w.realEmail && me.signin_alerts !== false;
  const [busy, setBusy] = useState("");

  const setAlerts = async (want: boolean, undo = true) => {
    setBusy("alerts");
    setMe({ signin_alerts: want });
    try {
      await api.me.update({ signin_alerts: want });
      toast(want ? "Sign-in alerts on" : "Sign-in alerts off", undo ? { undo: { label: "Undo", run: () => void setAlerts(!want, false) } } : undefined);
    } catch (e) {
      setMe({ signin_alerts: !want });
      if ((e as { status?: number }).status !== 401) toast(accountErr(e, w, "Could not save"), { bad: true });
    } finally {
      setBusy("");
    }
  };
  const setWait = async (want: boolean, undo = true) => {
    setBusy("wait");
    try {
      const r = await api.hack.setEmailUpdates(want);
      qc.setQueryData(["preferences"], r);
      toast("Saved", undo ? { undo: { label: "Undo", run: () => void setWait(!want, false) } } : undefined);
    } catch (e) {
      if ((e as { status?: number }).status !== 401) toast(accountErr(e, w, "Could not save"), { bad: true });
    } finally {
      setBusy("");
    }
  };
  const p = prefs.data;
  const NO_EMAIL = "Your account has no email address, so there's nothing to email.";
  return (
    <Section title="Preferences">
      <div className="max-w-prose divide-y divide-rule">
        <Check
          checked={alertsOn}
          disabled={!w.realEmail || busy === "alerts"}
          onChange={(on) => void setAlerts(on)}
          hint={w.realEmail ? "An email after each new sign-in or connected app, so you notice one that wasn’t you." : NO_EMAIL}
        >
          Email me about new sign-ins
        </Check>
        <Check
          checked={!!(p?.email_updates && p.email_possible)}
          disabled={!p?.email_possible || busy === "wait"}
          onChange={(on) => void setWait(on)}
          hint={
            p && !p.email_possible
              ? NO_EMAIL
              : "Team requests and invites, application answers, and changes to your team or its site. Organisers also hear about new applications. At most one email of each kind every 15 minutes."
          }
        >
          Email me when something waits on me
        </Check>
      </div>
    </Section>
  );
}

// ---- The screen ----

function Body({ me }: { me: Me }) {
  const { hash } = useLocation();
  const navigate = useNavigate();
  const setMe = useSetMe();
  const qc = useQueryClient();
  const w = words(me);
  const [ask, askDialog] = useAsk(w);
  const tab = TABS.some(([id]) => "#" + id === hash) ? hash.slice(1) : "profile";
  return (
    <>
      <Tabs value={tab} onValueChange={(v) => navigate({ hash: v === "profile" ? "" : v }, { replace: true })} className="mt-5">
        <TabsList aria-label="Account">
          {TABS.map(([id, label]) => (
            <TabsTrigger key={id} value={id}>
              {label}
            </TabsTrigger>
          ))}
        </TabsList>
        <TabsContent value="profile" forceMount className="data-[state=inactive]:hidden">
          <NamePart me={me} w={w} />
          <EmailPart
            w={w}
            onChanged={(next) => {
              setMe({ username: next, email: next });
              void qc.invalidateQueries({ queryKey: ["identities"] });
            }}
          />
          {me.has_password === true && <PasswordPart w={w} />}
          <Danger title="Danger zone">
            <DeleteAccount w={w} />
          </Danger>
        </TabsContent>
        <TabsContent value="sign-in" forceMount className="data-[state=inactive]:hidden">
          <GooglePart w={w} ask={ask} />
          <Danger title="Danger zone">
            <KeyRows ask={ask} />
          </Danger>
        </TabsContent>
        <TabsContent value="apps" forceMount className="data-[state=inactive]:hidden">
          <ConnectApps w={w} ask={ask} />
        </TabsContent>
        <TabsContent value="emails" forceMount className="data-[state=inactive]:hidden">
          <EmailsPart me={me} w={w} />
        </TabsContent>
      </Tabs>
      {askDialog}
      <LeaveGuard />
    </>
  );
}

export default function Account() {
  const q = useMe();
  const title = <PageTitle>Account</PageTitle>;
  if (q.isPending)
    return (
      <>
        {title}
        <div className="mt-6" role="status" aria-busy="true" aria-label="Loading your account">
          <Skeleton className="h-11 w-80 max-w-full" />
          <Skeleton className="mt-6 h-24 w-full" />
          <Skeleton className="mt-3 h-24 w-full" />
        </div>
      </>
    );
  if (q.isError)
    return (
      <>
        {title}
        <Failed error={q.error} retry={() => q.refetch()} />
      </>
    );
  const me = q.data as Me;
  if (!canOpen(me))
    return (
      <>
        {title}
        <EmptyState title="Sign in with your email to open your account." />
      </>
    );
  return (
    <>
      {title}
      <Body me={me} />
    </>
  );
}
