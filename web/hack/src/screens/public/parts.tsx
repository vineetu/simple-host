// What the link pages share: their header, the code of conduct, the name
// line, the signed-out step, and how they load (a stale key on a link page
// is dropped and the page loads again signed out, as app.js signedOut does).
import type { ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { Button } from "@kit/components/ui/button";
import { Card } from "@kit/components/ui/card";
import { Field, Input } from "@kit/components/ui/input";
import { Notice } from "@kit/components/ui/notice";
import { Skeleton } from "@kit/components/ui/skeleton";
import type { ApiError } from "@kit/api/call";
import { useHack, type EventRow, type Me } from "@/lib/client";
import { apiKey, goSignIn } from "@/lib/session";

export const eventPage = (slug?: string) => "/e/" + encodeURIComponent(slug || "");

/** useLink loads a link page's answer; a refused key is forgotten and the page asks again, signed out. */
export function useLink<T>(key: unknown[], load: () => Promise<T>) {
  return useQuery({
    queryKey: ["link", ...key],
    retry: false,
    queryFn: async () => {
      try {
        return await load();
      } catch (e) {
        if ((e as ApiError).status === 401) return await load();
        throw e;
      }
    },
  });
}

/** useAccount is the signed-in person and their events (nothing is asked for signed out). */
export function useAccount() {
  const api = useHack();
  const on = !!apiKey();
  const me = useQuery<Me>({ queryKey: ["me"], queryFn: () => api.me.get(), enabled: on, staleTime: 60_000 });
  const events = useQuery<EventRow[]>({ queryKey: ["events"], queryFn: () => api.hack.events(), enabled: on });
  return { signedIn: on, me: me.data, events: events.data, ready: !on || (!me.isPending && !events.isPending) };
}

export function LinkLoading({ label }: { label: string }) {
  return (
    <div className="mx-auto mt-8 max-w-prose" role="status" aria-busy="true" aria-label={label}>
      <Skeleton className="h-9 w-72 max-w-full" />
      <Skeleton className="mt-3 h-5 w-56" />
      <Skeleton className="mt-8 h-56 w-full" />
    </div>
  );
}

/** Page is a link page's column: its heading, the lines under it, then the rest. */
export function Page({ title, lines, children }: { title: string; lines?: ReactNode[]; children?: ReactNode }) {
  return (
    <div className="mx-auto max-w-prose">
      <div className="mt-8 mb-6">
        <h1 className="type-page">{title}</h1>
        {lines?.filter(Boolean).map((l, i) => (
          <p key={i} className="mt-1 text-ink-soft">
            {l}
          </p>
        ))}
      </div>
      {children}
    </div>
  );
}

/** Problem is a load that did not work: the line, in the bad tone. */
export function Problem({ title = "This link did not open", text }: { title?: string; text: string }) {
  return (
    <Page title={title}>
      <Notice tone="bad" title={text} />
    </Page>
  );
}

/** Actions is a row of buttons under a line. */
export function Actions({ children }: { children: ReactNode }) {
  return <div className="mt-4 flex flex-wrap gap-2.5">{children}</div>;
}

export function LinkButton({ href, children, primary }: { href: string; children: ReactNode; primary?: boolean }) {
  return (
    <Button variant={primary ? "primary" : "secondary"} asChild>
      <a href={href}>{children}</a>
    </Button>
  );
}

/** Coc is the platform's code of conduct and the organisers' own words. */
export function Coc({ text, extra, heading = "Code of conduct" }: { text?: string; extra?: string; heading?: string }) {
  return (
    <div className="mt-6">
      <h2 className="type-section">{heading}</h2>
      {text && <p className="mt-1.5 whitespace-pre-line text-ink-soft">{text}</p>}
      {extra?.trim() && (
        <>
          <h3 className="mt-4 text-base font-medium">From the organisers</h3>
          <p className="mt-1 whitespace-pre-line text-ink-soft">{extra.trim()}</p>
        </>
      )}
    </div>
  );
}

/** NameLine is who the person will appear as, or the field asking for a name when the account has none. */
export function NameLine({ name, id, value, onChange, error }: { name: string; id: string; value: string; onChange: (v: string) => void; error?: string }) {
  if (name)
    return (
      <p className="mb-4 text-ink-soft">
        You'll appear as <span className="text-ink">{name}</span>. Change it on <a href="/account">Account</a>.
      </p>
    );
  return (
    <Field id={id} label="Your name" hint="Saved to your account and shown in every event you join." error={error}>
      <Input
        id={id}
        maxLength={100}
        autoComplete="name"
        value={value}
        aria-invalid={!!error}
        aria-describedby={id + "-msg"}
        onChange={(e) => onChange(e.target.value)}
      />
    </Field>
  );
}

/** SignInStep is the signed-out card: what happens, then Sign in (which comes back to this link). */
export function SignInStep({ title, children, label = "Sign in", after }: { title?: string; children?: ReactNode; label?: string; after?: ReactNode }) {
  return (
    <Card className="mt-6">
      {title && <h2 className="mb-2 type-section">{title}</h2>}
      {children}
      <Button variant="primary" size="lg" className="mt-5 md:w-auto md:px-8" onClick={() => goSignIn()}>
        {label}
      </Button>
      {after}
    </Card>
  );
}
