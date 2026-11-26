// Sign in in every state. Signed out (the key is cleared first), since a
// signed-in browser goes straight on.
import { refuse, type Entry, type Route } from "@kit/catalogue/harness";
import { api } from "@/catalogue/fixtures";

const out = () => localStorage.removeItem("apiKey");
const PROV = "/v1/auth/oauth/providers";
const withProviders = (p: unknown, over: Record<string, unknown> = {}): Route => api({ [PROV]: p, ...over });
const emailOnly = { providers: [], email_enabled: true };
const everything = { providers: ["google", "oidc"], email_enabled: true, oidc_label: "Acme SSO", password: { enabled: true, self_signup: true } };
const toCode = [{ fill: ["#signin-email", "siti@gmail.com"] as [string, string] }, { click: "text=Continue" }];

export const entries: Entry[] = [
  { id: "signin.loading", title: "Sign in: finding the ways in", url: "/signin", api: api({ [PROV]: "pending" }), before: out },
  { id: "signin.email", title: "Sign in: the emailed code", url: "/signin?next=%2Fevents", api: withProviders(emailOnly), before: out },
  { id: "signin.google", title: "Sign in: Google or the emailed code", url: "/signin", api: withProviders({ providers: ["google"], email_enabled: true }), before: out },
  { id: "signin.everything", title: "Sign in: Google, company account, password and code", url: "/signin", api: withProviders(everything), before: out },
  {
    id: "signin.company",
    title: "Sign in: a company install, no email codes",
    url: "/signin",
    api: withProviders({ providers: ["oidc"], email_enabled: false, oidc_label: "Acme SSO" }),
    before: out,
  },
  { id: "signin.password-only", title: "Sign in: password only", url: "/signin", api: withProviders({ providers: [], email_enabled: false, password: { enabled: true } }), before: out },
  {
    id: "signin.password-signup",
    title: "Sign in: creating an account with a password",
    url: "/signin",
    api: withProviders({ providers: [], email_enabled: false, password: { enabled: true, self_signup: true } }),
    before: out,
    steps: [{ click: "text=Create an account" }],
  },
  {
    id: "signin.password-wrong",
    title: "Sign in: wrong password",
    url: "/signin",
    api: withProviders({ providers: [], email_enabled: false, password: { enabled: true } }, { "POST /v1/auth/password": refuse(401, "bad_credentials", "Wrong username or password.") }),
    before: out,
    steps: [{ fill: ["#signin-login", "siti"] }, { fill: ["#signin-password", "not-it-at-all"] }, { click: "text=Sign in" }],
  },
  {
    id: "signin.password-change",
    title: "Sign in: a new password is needed first",
    url: "/signin",
    api: withProviders(
      { providers: [], email_enabled: false, password: { enabled: true } },
      { "POST /v1/auth/password": refuse(403, "password_change_required", "Choose a new password to finish signing in.") },
    ),
    before: out,
    steps: [{ fill: ["#signin-login", "siti"] }, { fill: ["#signin-password", "temporary-one"] }, { click: "text=Sign in" }],
  },
  { id: "signin.none", title: "Sign in: nothing turned on", url: "/signin", api: withProviders({ providers: [], email_enabled: false }), before: out },
  {
    id: "signin.bad-email",
    title: "Sign in: the email does not look right",
    url: "/signin",
    api: withProviders(emailOnly),
    before: out,
    steps: [{ fill: ["#signin-email", "siti@"] }, { click: "text=Continue" }],
  },
  {
    id: "signin.send-failed",
    title: "Sign in: the code could not be sent",
    url: "/signin",
    api: withProviders(emailOnly, { "POST /v1/auth": refuse(429, "rate_limited", "Too many tries.") }),
    before: out,
    steps: toCode,
  },
  { id: "signin.code", title: "Sign in: check your email", url: "/signin", api: withProviders(emailOnly), before: out, steps: toCode },
  {
    id: "signin.code-wrong",
    title: "Sign in: the code is not right",
    url: "/signin",
    api: withProviders(emailOnly, { "POST /v1/auth/verify": refuse(401, "", "That code is not right.", { reason: "code_wrong", tries_left: 2 }) }),
    before: out,
    steps: [...toCode, { fill: ["#signin-code", "123456"] }],
  },
  {
    id: "signin.code-used-up",
    title: "Sign in: too many wrong tries",
    url: "/signin",
    api: withProviders(emailOnly, { "POST /v1/auth/verify": refuse(401, "", "Too many wrong tries.", { reason: "code_used_up" }) }),
    before: out,
    steps: [...toCode, { fill: ["#signin-code", "123456"] }],
  },
  {
    id: "signin.link-elsewhere",
    title: "Sign in: a link opened in another browser",
    url: "/signin?token=abc&cn=not-this-browser",
    api: withProviders(emailOnly),
    before: out,
  },
];
