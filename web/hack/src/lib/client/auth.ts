// Auth / session: sign-in (screens/public/SignIn.tsx), the SSO cookie, signing out.
import type { Call } from "@kit/api/call";
import type { Untyped } from "./_e";

export type Providers = { providers?: string[]; email_enabled?: boolean; oidc_label?: string; password?: { enabled?: boolean } };
export const requestCode = (call: Call, email: string, nonce_hash?: string) => call<Untyped>("POST", "/v1/auth", nonce_hash ? { email, nonce_hash } : { email });
export const verify = (call: Call, body: Untyped) => call<{ api_key: string } & Untyped>("POST", "/v1/auth/verify", body);
export const password = (call: Call, body: Untyped) => call<{ api_key: string } & Untyped>("POST", "/v1/auth/password", body);
export const passwordSignup = (call: Call, body: Untyped) => call<{ api_key: string } & Untyped>("POST", "/v1/auth/password/signup", body);
export const providers = (call: Call) => call<Providers>("GET", "/v1/auth/oauth/providers");
/** The OAuth start is a page load, not a call: /v1/auth/oauth/{provider}?return_to=… */
export const oauthStart = (provider: string, returnTo: string) => "/v1/auth/oauth/" + encodeURIComponent(provider) + "?return_to=" + encodeURIComponent(returnTo);
export const ssoSession = (call: Call) => call<Untyped>("POST", "/v1/sso/session");
export const signOut = (call: Call) => call<Untyped>("POST", "/v1/me/sign-out");
