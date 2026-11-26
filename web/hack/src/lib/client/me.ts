// Me: the signed-in account (profile, email, password, Google links, key, AI apps, delete).
import type { Call } from "@kit/api/call";
import { seg, type Untyped } from "./_e";

export type Me = { id?: string; email?: string; display_name?: string; login?: string; username?: string; operator?: boolean } & Untyped;
export const get = (call: Call) => call<Me>("GET", "/v1/me");
export const update = (call: Call, body: Untyped) => call<Me>("PATCH", "/v1/me", body);
export const remove = (call: Call, body: Untyped = {}) => call<Untyped>("DELETE", "/v1/me", body);
export const changeEmail = (call: Call, email: string) => call<Untyped>("POST", "/v1/me/email", { email });
export const verifyEmail = (call: Call, body: Untyped) => call<Untyped>("POST", "/v1/me/email/verify", body);
export const setPassword = (call: Call, body: Untyped) => call<Untyped>("POST", "/v1/me/password", body);
export const identities = (call: Call) => call<Untyped>("GET", "/v1/me/identities");
export const removeIdentity = (call: Call, id: string) => call<Untyped>("DELETE", "/v1/me/identities/" + seg(id));
export const rotateKey = (call: Call) => call<{ api_key: string } & Untyped>("POST", "/v1/me/api-key/rotate");
export const connections = (call: Call) => call<Untyped>("GET", "/v1/me/connections");
export const removeConnection = (call: Call, id: string) => call<Untyped>("DELETE", "/v1/me/connections/" + seg(id));
