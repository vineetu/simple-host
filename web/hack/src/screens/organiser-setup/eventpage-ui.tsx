// The parts every Event page tab shares: what a tab is given, its heading,
// the notes (own website live, ended), the content document and its write,
// the "Saved" tick, and a file picker that looks like a button.
import { useCallback, useEffect, useRef, useState, type ChangeEvent, type ReactNode } from "react";
import { useQuery, useQueryClient, type UseQueryResult } from "@tanstack/react-query";
import { Button } from "@kit/components/ui/button";
import { Notice } from "@kit/components/ui/notice";
import { useToast } from "@kit/components/ui/toast";
import { cn } from "@kit/lib/utils";
import { useHack, type EventDetail } from "@/lib/client";
import { errorText, type Ev } from "./common";
import { doc, type Content, type ContentAnswer, type Website } from "./eventpage-data";

export type TabProps = {
  slug: string;
  d: EventDetail;
  ev: Ev;
  /** A platform admin's inspection: nothing changes. */
  admin: boolean;
  /** The event has ended (archived). */
  ended: boolean;
  content: UseQueryResult<ContentAnswer>;
  website: UseQueryResult<Website>;
};

export const useContent = (slug: string) => {
  const api = useHack();
  return useQuery({ queryKey: ["content", slug], queryFn: () => api.content.get(slug) as Promise<ContentAnswer> });
};
export const useWebsite = (slug: string) => {
  const api = useHack();
  return useQuery({ queryKey: ["website", slug], queryFn: () => api.content.website(slug) as Promise<Website> });
};

/**
 * usePersist writes the whole content document (PUT /content) and keeps
 * the server's answer; a refusal is a toast. It answers whether it saved.
 */
export function usePersist(slug: string) {
  const api = useHack();
  const qc = useQueryClient();
  const toast = useToast();
  return useCallback(
    async (next: Content, opts: { quiet?: boolean } = {}): Promise<boolean | string> => {
      try {
        const r = (await api.content.set(slug, { ...next })) as ContentAnswer;
        qc.setQueryData(["content", slug], r);
        return true;
      } catch (e) {
        if (opts.quiet) return errorText(e);
        toast(errorText(e), { bad: true });
        return false;
      }
    },
    [api, qc, slug, toast],
  );
}

/** current is the content document as last read. */
export const current = (c: UseQueryResult<ContentAnswer>): Content => doc(c.data);

export function TabHead({ title, sub, action }: { title: string; sub: ReactNode; action?: ReactNode }) {
  return (
    <div className="flex flex-wrap items-start gap-x-4 gap-y-3">
      <div className="min-w-0 flex-[1_1_18rem]">
        <h2 className="type-section">{title}</h2>
        <p className="mt-0.5 text-sm text-ink-soft">{sub}</p>
      </div>
      {action && <div className="flex flex-none flex-wrap gap-2">{action}</div>}
    </div>
  );
}

/** CustomSiteNote: while the organiser's own website is live, say so (and, where edits still save, that they do). */
export function CustomSiteNote({ website, readOnly }: { website: Website | undefined; readOnly: boolean }) {
  if (!(website?.mode === "custom" && website.has_custom_page)) return null;
  return <Notice title="Your own website is live at your address.">{readOnly ? null : "This still saves, and shows again if you switch back to the built-in page."}</Notice>;
}

export const EndedEditable = () => <Notice title="This event has ended. You can still fix the page text, colour and style." />;
export const EndedReadOnly = ({ what }: { what: string }) => <Notice title={`This event has ended. ${what} can’t change any more.`} />;

/** ContentFailed is the list tabs' note when sponsors, FAQ and schedule did not load. */
export function ContentFailed({ content }: { content: UseQueryResult<ContentAnswer> }) {
  return (
    <Notice
      tone="bad"
      title="Sponsors, FAQ and schedule could not be loaded."
      action={
        <Button onClick={() => content.refetch()} disabled={content.isFetching}>
          Reload
        </Button>
      }
    >
      Reload to edit them.
    </Notice>
  );
}

/** useSavedTick shows "Saved" beside a setting for a few seconds after it saved. */
export function useSavedTick(): [boolean, () => void] {
  const [on, setOn] = useState(false);
  const t = useRef<ReturnType<typeof setTimeout>>(undefined);
  useEffect(() => () => clearTimeout(t.current), []);
  return [
    on,
    () => {
      setOn(true);
      clearTimeout(t.current);
      t.current = setTimeout(() => setOn(false), 3000);
    },
  ];
}

export function SavedTick({ on }: { on: boolean }) {
  return (
    <span role="status" className={cn("inline-flex items-center gap-1 text-sm text-good", !on && "sr-only")}>
      {on && (
        <>
          <svg viewBox="0 0 20 20" className="size-4" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
            <path d="M4 10.5l4 4 8-9" />
          </svg>
          Saved
        </>
      )}
    </span>
  );
}

/** FilePick is a file input drawn as a button (the input covers the button, so it is the tap target). */
export function FilePick({ label, accept, onFile, disabled, primary }: { label: string; accept: string; onFile: (f: File) => void; disabled?: boolean; primary?: boolean }) {
  const pick = (e: ChangeEvent<HTMLInputElement>) => {
    const f = e.target.files?.[0];
    e.target.value = "";
    if (f) onFile(f);
  };
  return (
    <Button asChild variant={primary ? "primary" : "secondary"} className={cn("relative", disabled && "pointer-events-none opacity-55")}>
      <label>
        {label}
        <input type="file" accept={accept} aria-label={label} disabled={disabled} onChange={pick} className="absolute -inset-0.5 cursor-pointer opacity-0" />
      </label>
    </Button>
  );
}

/** Pill is a small word on a row (Now, Next, Fixed, Live). */
export function Pill({ children, tone }: { children: ReactNode; tone?: "accent" | "ok" }) {
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1 rounded-tag border px-2 text-xs leading-normal whitespace-nowrap",
        tone === "accent" ? "border-accent text-accent" : tone === "ok" ? "border-good text-good" : "border-ink-soft text-ink-soft",
      )}
    >
      {children}
    </span>
  );
}

export const PlusIcon = () => (
  <svg viewBox="0 0 20 20" className="size-4" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" aria-hidden="true">
    <path d="M10 4v12M4 10h12" />
  </svg>
);
