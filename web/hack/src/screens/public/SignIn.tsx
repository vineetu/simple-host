// Sign in (/signin): the old app's sign-in page and the platform card it
// mounted (static/hack/app.js routeSignIn, static/signin.js), on the kit.
// What the server offers decides the card: Google, a company account,
// username and password (with sign-up when the install allows it), and the
// emailed 6-digit code. An emailed link or a Google return arrives as
// ?token=&cn= and signs in here when this browser asked for it.
// Signing in keeps the old mechanics exactly: the key goes into this
// browser's localStorage ("apiKey") only after POST /v1/sso/session has
// bound the SSO cookie to it, then the page goes to ?next, or to the
// person's one current event, or Your events.
import { useEffect, useRef, useState, type FormEvent, type ReactNode } from "react";
import { useLocation, useNavigate } from "react-router";
import { Button } from "@kit/components/ui/button";
import { Card, CardLead, CardTitle, Fine } from "@kit/components/ui/card";
import { Field, FieldMessage, Input, Label } from "@kit/components/ui/input";
import { Skeleton } from "@kit/components/ui/skeleton";
import { paths, useHack } from "@/lib/client";
import { apiKey } from "@/lib/session";
import { eventHome } from "@/routes/resolve";
import type { Role } from "@/lib/client";
import { arrival, useAuth } from "./signin-auth";
import {
  anyMethod,
  companyCopy,
  consumeNext,
  digits,
  emailNonceHash,
  gmailURL,
  isGmail,
  landing,
  message,
  methods,
  nonceHash,
  providerNonce,
  readNext,
  reasonOf,
  takeNonceFor,
  validEmail,
  type Methods,
} from "./signin";

const ELSEWHERE = "Open this link on the device where you asked for it, or type the 6-digit code from the email.";

function Frame({ children }: { children: ReactNode }) {
  return <div className="mx-auto mt-8 w-full max-w-card md:mt-14">{children}</div>;
}

function Divider() {
  return (
    <div className="my-4.5 flex items-center gap-3 text-sm text-ink-faint before:flex-1 before:border-t before:border-rule after:flex-1 after:border-t after:border-rule">
      or
    </div>
  );
}

export default function SignIn() {
  const auth = useAuth();
  const api = useHack();
  const { pathname, search } = useLocation();
  const navigate = useNavigate();

  const [phase, setPhase] = useState<"verifying" | "card">("card");
  const [cfg, setCfg] = useState<Methods | null>(null);
  const [elsewhere, setElsewhere] = useState(false);
  const [step, setStep] = useState<"start" | "code">("start");
  const [email, setEmail] = useState("");
  const [fieldErr, setFieldErr] = useState("");
  const [startMsg, setStartMsg] = useState("");
  const [code, setCode] = useState("");
  const [codeErr, setCodeErr] = useState("");
  const [codeReason, setCodeReason] = useState("");
  const [signingIn, setSigningIn] = useState(false);
  const [busy, setBusy] = useState(false);
  const [resendAt, setResendAt] = useState(0);
  const [, tick] = useState(0);
  const [signup, setSignup] = useState(false);
  const [change, setChange] = useState(false);
  const [login, setLogin] = useState("");
  const [password, setPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const lastSubmitted = useRef("");
  const emailRef = useRef<HTMLInputElement>(null);
  const codeRef = useRef<HTMLInputElement>(null);
  const started = useRef(false);

  const complete = async (data: { api_key: string }) => {
    await auth.sso(data.api_key);
    try {
      localStorage.setItem("apiKey", data.api_key);
    } catch {
      /* storage blocked */
    }
    const next = consumeNext(search);
    if (next) return location.replace(next);
    let to = "/events";
    try {
      to = landing(await api.hack.events(), (role, slug) => eventHome(role as Role, slug));
    } catch {
      /* Your events, then */
    }
    location.replace(to);
  };

  const present = async () => {
    setPhase("card");
    setCfg(methods(await auth.providers().catch(() => null)));
  };

  // Once: keep ?next, take a link's token and cn off the address, sign in
  // with the link when this browser asked for it, else show the card; a
  // browser already signed in goes straight on.
  useEffect(() => {
    if (started.current) return;
    started.current = true;
    readNext(search);
    const params = new URLSearchParams(search);
    const token = params.get("token");
    const cn = params.get("cn");
    if (token !== null || cn !== null) {
      params.delete("token");
      params.delete("cn");
      const q = params.toString();
      navigate(pathname + (q ? "?" + q : ""), { replace: true });
    }
    void (async () => {
      if (token) {
        setPhase("verifying");
        const nonce = await takeNonceFor(cn);
        if (!nonce) {
          setElsewhere(true);
          await present();
          return;
        }
        try {
          await complete(await auth.verify({ token, nonce }));
        } catch (e) {
          await present();
          setStartMsg(message(e));
        }
        return;
      }
      await present();
      if (!apiKey()) return;
      try {
        await api.me.get();
        location.replace(consumeNext(search) || landing(await api.hack.events(), (role, slug) => eventHome(role as Role, slug)));
      } catch {
        /* a refused key is forgotten by the client; stay and sign in */
      }
    })();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const wait = Math.max(0, Math.ceil((resendAt - Date.now()) / 1000));
  useEffect(() => {
    if (step !== "code" || wait <= 0) return;
    const t = setInterval(() => tick((n) => n + 1), 1000);
    return () => clearInterval(t);
  }, [step, wait]);

  const sendCode = async (again: boolean) => {
    const addr = email.trim().toLowerCase();
    if (!validEmail(addr)) {
      setFieldErr(message({ code: "invalid_email" }));
      emailRef.current?.focus();
      return;
    }
    setBusy(true);
    try {
      const r = (await auth.requestCode(addr, await emailNonceHash())) as { email?: string };
      setEmail(r?.email || addr);
      setCode("");
      setCodeErr("");
      setCodeReason("");
      setStartMsg("");
      setFieldErr("");
      lastSubmitted.current = "";
      setResendAt(Date.now() + 30_000);
      setStep("code");
      setTimeout(() => codeRef.current?.focus());
    } catch (e) {
      if (again && step === "code") setCodeErr(message(e));
      else {
        setStartMsg(message(e));
        setStep("start");
      }
    } finally {
      setBusy(false);
    }
  };

  const submitCode = async (value: string) => {
    const c = digits(value);
    if (c.length !== 6 || signingIn || c === lastSubmitted.current) return;
    lastSubmitted.current = c;
    setSigningIn(true);
    setCodeErr("");
    try {
      await complete(await auth.verify({ email: email.trim().toLowerCase(), code: c, name: "dashboard sign-in" }));
    } catch (e) {
      setCodeReason(reasonOf(e));
      setCodeErr(message(e));
      setSigningIn(false);
      setTimeout(() => codeRef.current?.select());
    }
  };

  const submitPassword = async (e: FormEvent) => {
    e.preventDefault();
    if (busy) return;
    setBusy(true);
    setStartMsg("");
    try {
      const body: Record<string, unknown> = signup ? { username: login, password } : { login, password, name: "dashboard sign-in" };
      if (change) body.new_password = newPassword;
      const data = signup ? await auth.passwordSignup(body) : await auth.password(body);
      setPassword("");
      setNewPassword("");
      await complete(data);
    } catch (err) {
      if ((err as { code?: string }).code === "password_change_required") setChange(true);
      setStartMsg(message(err));
    } finally {
      setBusy(false);
    }
  };

  const startProvider = async (provider: "google" | "oidc") => {
    readNext(search);
    const n = providerNonce(provider);
    if (n === null) {
      setStartMsg("This browser is blocking storage sign-in needs. Use the emailed code.");
      return;
    }
    const hash = await nonceHash(n);
    location.assign(paths.oauthStart(provider, arrival(location.origin + "/?cn=" + hash)));
  };

  if (phase === "verifying")
    return (
      <Frame>
        <Card aria-labelledby="signin-title">
          <CardTitle id="signin-title">Sign in</CardTitle>
          <p role="status" className="text-ink-soft">
            Signing in…
          </p>
        </Card>
      </Frame>
    );

  if (!cfg)
    return (
      <Frame>
        <Card aria-labelledby="signin-title" aria-busy="true">
          <CardTitle id="signin-title">Sign in</CardTitle>
          <div role="status" aria-label="Loading">
            <Skeleton className="mt-4 h-12 w-full" />
            <Skeleton className="mt-3 h-12 w-full" />
          </div>
        </Card>
      </Frame>
    );

  const note = elsewhere && (
    <p role="status" className="mb-4 rounded-field border-[1.5px] border-accent bg-accent-soft px-4 py-3 text-sm">
      {ELSEWHERE}
    </p>
  );

  if (!anyMethod(cfg))
    return (
      <Frame>
        {note}
        <Card aria-labelledby="signin-title">
          <CardTitle id="signin-title">Sign in</CardTitle>
          <CardLead className="mb-0">No way to sign in is turned on for this server. Ask whoever runs it.</CardLead>
        </Card>
      </Frame>
    );

  if (step === "code") {
    const usedUp = codeReason === "code_used_up";
    const expired = codeReason === "code_expired";
    return (
      <Frame>
        <Card aria-labelledby="signin-title">
          <CardTitle id="signin-title">Check your email</CardTitle>
          <CardLead>
            We sent a 6-digit code to <b className="text-ink">{email}</b>.{" "}
            <Button
              variant="link"
              onClick={() => {
                setStep("start");
                setCodeErr("");
                setCodeReason("");
                setStartMsg("");
                setSigningIn(false);
                setTimeout(() => emailRef.current?.focus());
              }}
            >
              Change
            </Button>
          </CardLead>
          <form
            noValidate
            onSubmit={(e) => {
              e.preventDefault();
              if (usedUp || expired) {
                setCodeReason("");
                void sendCode(true);
                return;
              }
              lastSubmitted.current = "";
              void submitCode(code);
            }}
          >
            <Label htmlFor="signin-code">Code</Label>
            <Input
              ref={codeRef}
              id="signin-code"
              inputMode="numeric"
              autoComplete="one-time-code"
              enterKeyHint="done"
              placeholder="······"
              value={code}
              onChange={(e) => {
                const v = digits(e.target.value);
                setCode(v);
                if (v.length === 6) void submitCode(v);
              }}
              aria-invalid={codeErr ? true : undefined}
              aria-describedby="signin-code-msg"
              className="h-15 pl-[.42em] text-center font-mono text-[30px] tracking-[.42em]"
            />
            <FieldMessage id="signin-code-msg" tone="bad">
              {codeErr}
            </FieldMessage>
            <Button type="submit" variant="primary" size="lg" className="mt-3" disabled={busy || (signingIn && !usedUp && !expired)}>
              {usedUp || expired ? "Send a new code" : signingIn ? "Signing you in…" : "Sign in"}
            </Button>
          </form>
          <div className="mt-3 flex min-h-11 flex-wrap items-center justify-between gap-2 text-sm text-ink-soft">
            <span>
              {usedUp ? null : wait > 0 ? (
                <>Resend in 0:{String(wait).padStart(2, "0")}</>
              ) : (
                <Button variant="link" className="inline-flex min-h-11 items-center" onClick={() => void sendCode(true)} disabled={busy}>
                  Resend code
                </Button>
              )}
            </span>
            {isGmail(email) && (
              <a href={gmailURL()} target="_blank" rel="noopener noreferrer" className="inline-flex min-h-11 items-center">
                Open Gmail<span className="sr-only"> (opens in a new tab)</span>
              </a>
            )}
          </div>
          <Fine>Not there? Check spam, or wait a minute. The link in the email signs you in too, on this device.</Fine>
        </Card>
      </Frame>
    );
  }

  const company = companyCopy(cfg);
  const pw = cfg.password?.enabled;
  const google = cfg.providers.includes("google");
  const oidc = cfg.providers.includes("oidc");
  return (
    <Frame>
      {note}
      <Card aria-labelledby="signin-title">
        <CardTitle id="signin-title">Sign in</CardTitle>
        <CardLead>{company?.lead || "New here? The same steps create your account."}</CardLead>
        {google && (
          <Button variant="google" size="lg" onClick={() => void startProvider("google")} disabled={busy}>
            <GoogleMark />
            <span>Continue with Google</span>
          </Button>
        )}
        {oidc && (
          <Button size="lg" className={google ? "mt-2.5" : undefined} onClick={() => void startProvider("oidc")} disabled={busy}>
            Continue with {cfg.oidcLabel || "your company account"}
          </Button>
        )}
        {pw && (
          <>
            {(google || oidc) && <Divider />}
            <form noValidate onSubmit={submitPassword}>
              <Field id="signin-login" label="Email or username">
                <Input id="signin-login" autoComplete="username" placeholder="you@example.com" value={login} onChange={(e) => setLogin(e.target.value)} />
              </Field>
              <Field id="signin-password" label="Password">
                <Input
                  id="signin-password"
                  type="password"
                  minLength={8}
                  autoComplete={signup ? "new-password" : "current-password"}
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                />
              </Field>
              {change && (
                <Field id="signin-new-password" label="New password">
                  <Input id="signin-new-password" type="password" minLength={8} autoComplete="new-password" value={newPassword} onChange={(e) => setNewPassword(e.target.value)} />
                </Field>
              )}
              <Button type="submit" variant={cfg.emailOn ? "secondary" : "primary"} size="lg" disabled={busy}>
                {change ? "Change password and sign in" : signup ? "Create account" : "Sign in"}
              </Button>
              {cfg.password?.self_signup && !change && (
                <Button
                  variant="link"
                  className="mt-2 flex min-h-11 items-center"
                  onClick={() => {
                    setSignup(!signup);
                    setStartMsg("");
                  }}
                >
                  {signup ? "Sign in instead" : "Create an account"}
                </Button>
              )}
            </form>
          </>
        )}
        {cfg.emailOn && (
          <>
            {(pw || google || oidc) && <Divider />}
            <form
              noValidate
              onSubmit={(e) => {
                e.preventDefault();
                setStartMsg("");
                void sendCode(false);
              }}
            >
              <Label htmlFor="signin-email">Email</Label>
              <Input
                ref={emailRef}
                id="signin-email"
                type="email"
                autoComplete="email"
                placeholder="you@example.com"
                value={email}
                onChange={(e) => {
                  setEmail(e.target.value);
                  setFieldErr("");
                }}
                aria-invalid={fieldErr ? true : undefined}
                aria-describedby="signin-email-msg"
              />
              <FieldMessage id="signin-email-msg" tone="bad">
                {fieldErr}
              </FieldMessage>
              <Button type="submit" variant="primary" size="lg" className="mt-3" disabled={busy}>
                Continue
              </Button>
            </form>
          </>
        )}
        {company ? (
          <Fine>{company.fine}</Fine>
        ) : cfg.emailOn ? (
          <Fine>
            We email you a 6-digit code. No password to remember. By continuing you agree to the <a href="/terms">Terms</a> and <a href="/privacy.html">Privacy</a>.
          </Fine>
        ) : null}
        {startMsg && (
          <p role="alert" className="mt-4 flex items-start gap-2 rounded-field border-[1.5px] border-bad px-3 py-2 text-sm text-bad">
            <span aria-hidden="true">⚠</span>
            <span>{startMsg}</span>
          </p>
        )}
      </Card>
    </Frame>
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
