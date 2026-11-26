// Sign-up and people: applications and approval, people, co-organisers.
import type { Call } from "@kit/api/call";
import { E, seg, type Untyped } from "./_e";

export const registration = (call: Call, slug: string) => call<Untyped>("GET", E(slug, "registration"));
export const setRegistration = (call: Call, slug: string, body: Untyped) => call<Untyped>("PUT", E(slug, "registration"), body);
export const applyForm = (call: Call, slug: string) => call<Untyped>("GET", E(slug, "apply"));
export const apply = (call: Call, slug: string, body: Untyped) => call<Untyped>("POST", E(slug, "apply"), body);
export const applications = (call: Call, slug: string) => call<Untyped>("GET", E(slug, "applications"));
export const decide = (call: Call, slug: string, user: string, body: Untyped) => call<Untyped>("POST", E(slug, "applications", seg(user), "decision"), body);
export const list = (call: Call, slug: string) => call<Untyped>("GET", E(slug, "people"));
export const remove = (call: Call, slug: string, user: string) => call<Untyped>("DELETE", E(slug, "people", seg(user)));
export const createOrganiserInvite = (call: Call, slug: string, body: Untyped = {}) => call<Untyped>("POST", E(slug, "organiser-invite"), body);
export const revokeOrganiserInvite = (call: Call, slug: string) => call<Untyped>("DELETE", E(slug, "organiser-invite"));
export const removeOrganiser = (call: Call, slug: string, user: string) => call<Untyped>("DELETE", E(slug, "organisers", seg(user)));
