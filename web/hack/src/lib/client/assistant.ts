// The organiser's Ask panel (off unless the server enables it). POST streams (SSE),
// so the screen reads it with fetch directly; this is the status read.
import type { Call } from "@kit/api/call";
import { E, type Untyped } from "./_e";
export const status = (call: Call, slug: string) => call<{ enabled?: boolean; remaining_today?: number } & Untyped>("GET", E(slug, "assistant"));
export const askPath = (slug: string) => E(slug, "assistant");
