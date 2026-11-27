// Sign-in (route /signin): the emailed code in two steps, Google when the
// install has it, and the visitor's hop from a site.
//   ?next=<path>  where to go after signing in (default the dashboard)
//   ?hop=<token>  a visitor from a site: once the email is proved the answer
//                 carries next (the site's finish address); a browser already
//                 signed in here is asked once per site: "Continue as <email> to <site>?"
// On the way to approving an AI app (next=/oauth/authorize...), when the
// install has the plugin reviewer's account, "Other ways to sign in" holds
// its email and password form (access/signin/review.go).
import { useEffect, useRef, useState, type FormEvent } from "react";
import { useSearchParams } from "react-router";
import { ApiError, googleOn, reviewOn, useApi } from "@/lib/api";
import { localPath } from "@kit/lib/format";
import { Button } from "@kit/components/ui/button";
import { Card, CardLead, CardTitle, Fine } from "@kit/components/ui/card";
import { FieldMessage, Input, Label } from "@kit/components/ui/input";
import { PageShell } from "@kit/components/ui/page-shell";

type Verified = { email?: string; next?: string; confirm?: string };
type Say = { text: string; bad?: boolean };

export function SignIn() {
  const call = useApi();
  const [q] = useSearchParams();
  const hop = q.get("hop") || "";
  const nextParam = q.get("next") || "/dashboard";
  const next = localPath(nextParam) ? nextParam : "/dashboard";

  const [step, setStep] = useState<"email" | "code" | "ask">("email");
  const [email, setEmail] = useState("");
  const [code, setCode] = useState("");
  const [ask, setAsk] = useState({ who: "", site: "" });
  const [say, setSay] = useState<Say>({ text: "" });
  const [busy, setBusy] = useState(false);
  const [sentAt, setSentAt] = useState(0);
  const [, tick] = useState(0);
  const emailRef = useRef<HTMLInputElement>(null);
  const codeRef = useRef<HTMLInputElement>(null);
  const [rvEmail, setRvEmail] = useState("");
  const [rvPassword, setRvPassword] = useState("");
  const [rvSay, setRvSay] = useState<Say>({ text: "" });
  const showReview = !hop && reviewOn() && next.startsWith("/oauth/authorize");

  const after = (b: Verified) => {
    if (b.next) return window.location.replace(b.next);
    if (b.confirm) {
      setAsk({ who: b.email || "", site: b.confirm });
      setStep("ask");
      setSay({ text: "" });
      return;
    }
    window.location.replace(next);
  };

  // Already signed in here? Then a visitor only confirms.
  useEffect(() => {
    if (!hop) return;
    call<Verified>("POST", "/v1/auth/verify", { hop })
      .then(after)
      .catch((e: ApiError) => {
        if (e.code !== "no_code" && e.code !== "sign_in") setSay({ text: e.message, bad: true });
      });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [hop]);

  useEffect(() => {
    if (step !== "code") return;
    codeRef.current?.focus();
    const t = setInterval(() => tick((n) => n + 1), 1000); // the "new code in 0:29" line
    return () => clearInterval(t);
  }, [step]);
  const wait = Math.max(0, Math.ceil((sentAt + 30_000 - Date.now()) / 1000));

  const resend = async () => {
    setBusy(true);
    try {
      await call("POST", "/v1/auth/code", { email: email.trim() });
      setSentAt(Date.now());
      setCode("");
      setSay({ text: "We sent a new code. The old one no longer works." });
      codeRef.current?.focus();
    } catch (err) {
      setSay({ text: (err as Error).message, bad: true });
    } finally {
      setBusy(false);
    }
  };

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    try {
      if (step === "email") {
        const v = email.trim();
        if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(v)) {
          setSay({ text: "That email address does not look right.", bad: true });
          emailRef.current?.focus();
          return;
        }
        setSay({ text: "Sending…" });
        await call("POST", "/v1/auth/code", { email: v });
        setSentAt(Date.now());
        setStep("code");
        setSay({ text: "" });
        return;
      }
      setSay({ text: "Checking…" });
      const body: Record<string, unknown> = { email: email.trim(), code: code.trim() };
      if (hop) body.hop = hop;
      after(await call<Verified>("POST", "/v1/auth/verify", body));
    } catch (err) {
      setSay({ text: (err as Error).message, bad: true });
    } finally {
      setBusy(false);
    }
  };

  const reviewSubmit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    try {
      setRvSay({ text: "Checking…" });
      await call("POST", "/v1/auth/reviewer", { email: rvEmail.trim(), password: rvPassword });
      window.location.replace(next);
    } catch (err) {
      setRvSay({ text: (err as Error).message, bad: true });
    } finally {
      setBusy(false);
    }
  };

  const confirm = async () => {
    setBusy(true);
    try {
      after(await call<Verified>("POST", "/v1/auth/verify", { hop, confirm: true }));
    } catch (err) {
      setSay({ text: (err as Error).message, bad: true });
      setBusy(false);
    }
  };

  const terms = (
    <>
      By continuing you agree to the <a href="/terms">Terms</a> and <a href="/privacy">Privacy Policy</a>.
    </>
  );
  const msgId = "signin-msg";

  return (
    <PageShell title="Sign in" center>
      <div className="w-full max-w-card">
        {step === "ask" ? (
          <Card aria-labelledby="ask-title">
            <CardTitle id="ask-title">Continue as</CardTitle>
            <strong className="mb-2.5 block text-lg">{ask.who}</strong>
            <CardLead>
              to <b className="text-ink">{ask.site}</b>
            </CardLead>
            <Fine className="mt-0 mb-1">The site owner can see the email you sign in with.</Fine>
            <Button variant="primary" size="lg" className="mt-3" onClick={confirm} disabled={busy}>
              Continue
            </Button>
            <Button
              size="lg"
              className="mt-2.5"
              onClick={() => {
                setStep("email");
                setSay({ text: "" });
                setTimeout(() => emailRef.current?.focus());
              }}
            >
              Use another email
            </Button>
            <Button asChild variant="link" className="mt-2.5">
              <a href="/">Cancel</a>
            </Button>
            <FieldMessage tone={say.bad ? "bad" : undefined}>{say.text}</FieldMessage>
          </Card>
        ) : (
          <Card aria-labelledby="signin-title">
            <CardTitle id="signin-title">{step === "code" ? "Check your email" : "Sign in"}</CardTitle>
            <CardLead>
              {step === "code" ? (
                <>
                  We sent a 6-digit code to <b className="text-ink">{email.trim()}</b>.{" "}
                  <Button
                    variant="link"
                    onClick={() => {
                      setCode("");
                      setStep("email");
                      setSay({ text: "" });
                      setTimeout(() => emailRef.current?.focus());
                    }}
                  >
                    Change
                  </Button>
                </>
              ) : hop ? (
                "to continue to the site. No password."
              ) : (
                "New here? The same steps create your account."
              )}
            </CardLead>
            {step === "email" && googleOn() && (
              <>
                <Button asChild variant="google" size="lg">
                  <a href={"/v1/auth/oauth/google?" + new URLSearchParams(hop ? { hop } : { next })}>
                    <GoogleMark />
                    <span>Continue with Google</span>
                  </a>
                </Button>
                <div className="my-4.5 flex items-center gap-3 text-sm text-ink-faint before:flex-1 before:border-t before:border-rule after:flex-1 after:border-t after:border-rule">
                  or
                </div>
              </>
            )}
            <form onSubmit={submit} noValidate>
              {step === "email" ? (
                <>
                  <Label htmlFor="email">Email</Label>
                  <Input
                    ref={emailRef}
                    id="email"
                    type="email"
                    autoComplete="email"
                    placeholder="you@example.com"
                    value={email}
                    onChange={(e) => setEmail(e.target.value)}
                    aria-invalid={say.bad || undefined}
                    aria-describedby={msgId}
                  />
                </>
              ) : (
                <>
                  <Label htmlFor="code">Code</Label>
                  <Input
                    ref={codeRef}
                    id="code"
                    inputMode="numeric"
                    autoComplete="one-time-code"
                    enterKeyHint="done"
                    maxLength={6}
                    placeholder="······"
                    value={code}
                    onChange={(e) => setCode(e.target.value)}
                    aria-invalid={say.bad || undefined}
                    aria-describedby={msgId}
                    className="h-15 pl-[.42em] text-center font-mono text-[30px] font-semibold tracking-[.42em]"
                  />
                </>
              )}
              <FieldMessage id={msgId} tone={say.bad ? "bad" : undefined}>
                {say.text}
              </FieldMessage>
              <Button type="submit" variant="primary" size="lg" className="mt-3" disabled={busy}>
                {step === "code" ? "Sign in" : "Continue"}
              </Button>
            </form>
            {step === "code" && (
              <p className="mt-3 text-sm text-ink-soft">
                {wait > 0 ? (
                  <>You can ask for a new code in 0:{String(wait).padStart(2, "0")}.</>
                ) : (
                  <Button variant="link" onClick={resend} disabled={busy}>
                    Send a new code
                  </Button>
                )}
              </p>
            )}
            <Fine>
              {step === "code" ? (
                "Not there? Check spam, or wait a minute."
              ) : hop ? (
                <>We email you a 6-digit code. {terms} The site owner can see the email you sign in with.</>
              ) : (
                <>We email you a 6-digit code. No password to remember. {terms}</>
              )}
            </Fine>
            {step === "email" && showReview && (
              <details className="mt-4 border-t border-rule pt-3 text-sm">
                <summary className="cursor-pointer text-ink-soft">Other ways to sign in</summary>
                <form onSubmit={reviewSubmit} noValidate className="mt-3">
                  <p className="mb-2 text-ink-soft">For app-directory reviewers using the demo account.</p>
                  <Label htmlFor="rv-email">Reviewer email</Label>
                  <Input id="rv-email" type="email" autoComplete="username" value={rvEmail} onChange={(e) => setRvEmail(e.target.value)} />
                  <Label htmlFor="rv-password">Password</Label>
                  <Input
                    id="rv-password"
                    type="password"
                    autoComplete="current-password"
                    value={rvPassword}
                    onChange={(e) => setRvPassword(e.target.value)}
                    aria-describedby="rv-msg"
                  />
                  <FieldMessage id="rv-msg" tone={rvSay.bad ? "bad" : undefined}>
                    {rvSay.text}
                  </FieldMessage>
                  <Button type="submit" size="lg" className="mt-3" disabled={busy}>
                    Sign in
                  </Button>
                </form>
              </details>
            )}
          </Card>
        )}
      </div>
    </PageShell>
  );
}

function GoogleMark() {
  return (
    <svg viewBox="0 0 48 48" aria-hidden="true" className="size-5 flex-none">
      <path fill="#EA4335" d="M24 9.5c3.54 0 6.71 1.22 9.21 3.6l6.85-6.85C35.9 2.38 30.47 0 24 0 14.62 0 6.51 5.38 2.56 13.22l7.98 6.19C12.43 13.72 17.74 9.5 24 9.5z" />
      <path fill="#4285F4" d="M46.98 24.55c0-1.57-.15-3.09-.38-4.55H24v9.02h12.94c-.58 2.96-2.26 5.48-4.78 7.18l7.73 6c4.51-4.18 7.09-10.36 7.09-17.65z" />
      <path fill="#FBBC05" d="M10.53 28.59c-.48-1.45-.76-2.99-.76-4.59s.27-3.14.76-4.59l-7.98-6.19C.92 16.46 0 20.12 0 24c0 3.88.92 7.54 2.56 10.78l7.97-6.19z" />
      <path fill="#34A853" d="M24 48c6.48 0 11.93-2.13 15.89-5.81l-7.73-6c-2.15 1.45-4.92 2.3-8.16 2.3-6.26 0-11.57-4.22-13.47-9.91l-7.98 6.19C6.51 42.62 14.62 48 24 48z" />
    </svg>
  );
}
