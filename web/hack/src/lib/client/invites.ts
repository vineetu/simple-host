// Invites: preview and accept the join, judge, co-organiser and team links.
import type { Call } from "@kit/api/call";
import { seg, type Untyped } from "./_e";

export const join = (call: Call, code: string) => call<Untyped>("GET", "/v1/hack/join/" + seg(code));
export const acceptJoin = (call: Call, code: string, body: Untyped = {}) => call<Untyped>("POST", "/v1/hack/join/" + seg(code), body);
export const judge = (call: Call, code: string) => call<Untyped>("GET", "/v1/hack/judge/" + seg(code));
export const acceptJudge = (call: Call, code: string, body: Untyped = {}) => call<Untyped>("POST", "/v1/hack/judge/" + seg(code), body);
export const organiser = (call: Call, code: string) => call<Untyped>("GET", "/v1/hack/organiser/" + seg(code));
export const acceptOrganiser = (call: Call, code: string, body: Untyped = {}) => call<Untyped>("POST", "/v1/hack/organiser/" + seg(code), body);
export const teamLink = (call: Call, code: string) => call<Untyped>("GET", "/v1/hack/team-links/" + seg(code));
