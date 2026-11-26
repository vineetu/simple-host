// What the organiser's setup screens share: the read-only rule and its note,
// the Save foot of a form, the ?tab= of a tabbed screen, refusal wording,
// byte uploads (icon, site zip), and the event-zone date fields.
import { useCallback, useEffect, type ReactNode } from "react";
import { useLocation, useNavigate } from "react-router";
import { useQueryClient } from "@tanstack/react-query";
import { ApiError } from "@kit/api/call";
import { Button } from "@kit/components/ui/button";
import { FieldMessage, Input, Label, NativeSelect } from "@kit/components/ui/input";
import { cn } from "@kit/lib/utils";
import type { EventDetail } from "@/lib/client";
import { apiKey, signedOut } from "@/lib/session";
import { isLocalMidnight, zoneOptions, zonedParts } from "@/lib/time";

export type Ev = EventDetail["event"] & Record<string, unknown>;

/** errorText is the line for a refusal: a code's own copy when given, else the server's sentence. */
export function errorText(err: unknown, codes: Record<string, string> = {}): string {
  if (err instanceof ApiError) {
    if (codes[err.code]) return codes[err.code];
    return err.message || "Something went wrong.";
  }
  return (err as Error)?.message || "Something went wrong.";
}

/** useRefreshEvent re-reads GET E (the frame's title, rail and status strip follow). */
export function useRefreshEvent(slug: string) {
  const qc = useQueryClient();
  return useCallback(() => qc.invalidateQueries({ queryKey: ["event", slug] }), [qc, slug]);
}

/**
 * useTab is the open tab of a tabbed screen, kept in ?tab= (Back steps
 * through tabs). An unknown or missing tab is the first; fromHash maps an
 * old #hash link to its tab, and the address is put right in place.
 */
export function useTab<T extends string>(tabs: readonly T[], fromHash: Record<string, T> = {}): [T, (t: T) => void] {
  const { pathname, search, hash } = useLocation();
  const navigate = useNavigate();
  const params = new URLSearchParams(search);
  let tab = params.get("tab") as T;
  if (!tabs.includes(tab)) {
    let h = "";
    try {
      h = decodeURIComponent(hash.slice(1));
    } catch {
      h = "";
    }
    tab = Object.hasOwn(fromHash, h) ? fromHash[h] : tabs[0];
  }
  const want = params.get("tab");
  useEffect(() => {
    if (want === tab) return;
    const p = new URLSearchParams(search);
    p.set("tab", tab);
    navigate(pathname + "?" + p.toString(), { replace: true });
  }, [want, tab, pathname, search, navigate]);
  const set = (t: T) => {
    const p = new URLSearchParams(search);
    p.set("tab", t);
    navigate(pathname + "?" + p.toString());
  };
  return [tab, set];
}

/**
 * SaveFoot is a form's foot: whether anything is unsaved, Discard (only
 * with changes) and Save (disabled until something changes). sticky keeps
 * it in reach at the bottom of a long form.
 */
export function SaveFoot({
  dirty,
  busy,
  onDiscard,
  saveLabel = "Save",
  error,
  sticky,
  disabled,
}: {
  dirty: boolean;
  busy?: boolean;
  onDiscard?: () => void;
  saveLabel?: string;
  error?: string;
  sticky?: boolean;
  disabled?: boolean;
}) {
  return (
    <div className={cn("mt-4 border-t border-rule pt-3", sticky && "sticky bottom-[calc(var(--sh-bottom-bar,0px)+env(safe-area-inset-bottom))] z-10 bg-card pb-3 md:bottom-0")}>
      {error && <FieldMessage tone="bad">{error}</FieldMessage>}
      <div className="flex flex-wrap items-center justify-end gap-2.5">
        <span className={cn("mr-auto text-sm", dirty ? "text-ink" : "text-ink-soft")} role="status">
          {dirty ? "Unsaved changes" : "No unsaved changes"}
        </span>
        {dirty && onDiscard && <Button onClick={onDiscard}>Discard</Button>}
        <Button type="submit" variant="primary" disabled={disabled || !dirty || busy}>
          {busy ? "Saving…" : saveLabel}
        </Button>
      </div>
    </div>
  );
}

/** Panel is one part of a setup tab: a card-edged block with its heading and a line under it. */
export function Panel({ title, sub, id, action, children, danger }: { title: string; sub?: ReactNode; id?: string; action?: ReactNode; children?: ReactNode; danger?: boolean }) {
  return (
    <section id={id} aria-label={title} className={cn("mt-5 max-w-prose rounded-field border-[1.5px] bg-card px-4 py-4 md:px-5", danger ? "border-bad" : "border-rule")}>
      <div className="flex flex-wrap items-start gap-x-4 gap-y-2">
        <div className="min-w-0 flex-[1_1_16rem]">
          <h2 className="type-section">{title}</h2>
          {sub && <p className="mt-0.5 text-sm text-ink-soft">{sub}</p>}
        </div>
        {action && <div className="flex flex-none flex-wrap gap-2">{action}</div>}
      </div>
      {children && <div className="mt-3">{children}</div>}
    </section>
  );
}

/** uploadBytes sends a file's bytes (the event icon, a site zip), signed as the old apiBytes did. */
export async function uploadBytes<T = Record<string, unknown>>(method: string, path: string, body: Blob | ArrayBuffer, contentType?: string): Promise<T> {
  const headers: Record<string, string> = { "X-SH-CSRF": "1" };
  const k = apiKey();
  if (k) headers["X-API-Key"] = k;
  if (contentType) headers["Content-Type"] = contentType;
  let res: Response;
  try {
    res = await fetch(path, { method, headers, body });
  } catch {
    throw new ApiError(0, "offline", "Cannot reach the server.");
  }
  const data = (await res.json().catch(() => ({}))) as Record<string, unknown>;
  if (res.status === 401) {
    signedOut();
    throw new ApiError(401, "signed_out", "You are signed out.");
  }
  if (!res.ok) {
    const code = typeof data.code === "string" && data.code ? data.code : "error";
    const msg = res.status >= 500 ? "Something went wrong. Try again." : typeof data.error === "string" && data.error ? data.error : "Something went wrong.";
    throw new ApiError(res.status, code, msg, data);
  }
  return data as T;
}

/** fileAsDataURL reads a picked file as a data: URL (sponsor logos travel inside JSON). */
export function fileAsDataURL(file: Blob): Promise<string> {
  return new Promise((ok, fail) => {
    const r = new FileReader();
    r.onload = () => ok(String(r.result || ""));
    r.onerror = () => fail(r.error || new Error("Could not read the file."));
    r.readAsDataURL(file);
  });
}

// ---- dates in the event's own zone (ui.js localInputValue, eventBoundary*)

/** localInput is what a date or datetime-local field shows for an instant, in the zone ('' for none). */
export function localInput(iso: string | null | undefined, zone?: string, dateOnly = false): string {
  const p = zonedParts(iso, zone);
  if (!p) return "";
  return dateOnly ? `${p.y}-${p.m}-${p.d}` : `${p.y}-${p.m}-${p.d}T${p.h}:${p.min}`;
}

/** boundaryInput splits an event start or end into its date and time fields (midnight shows no time). */
export function boundaryInput(iso: string | null | undefined, zone?: string): { date: string; time: string } {
  const local = localInput(iso, zone);
  return { date: local.slice(0, 10), time: iso && isLocalMidnight(iso, zone) ? "" : local.slice(11) };
}

/** boundaryValue is what Basics sends for a date and time pair (an empty time is local midnight). */
export const boundaryValue = (date: string, time: string) => (date ? date + "T" + (time || "00:00") : "");

/** ZoneSelect is the one time-zone select. */
export function ZoneSelect({ id, value, onChange, disabled }: { id: string; value: string; onChange: (z: string) => void; disabled?: boolean }) {
  return (
    <NativeSelect id={id} value={value} disabled={disabled} onChange={(e) => onChange(e.target.value)} className="w-full">
      {zoneOptions(value).map((z) => (
        <option key={z.zone} value={z.zone}>
          {z.label}
        </option>
      ))}
    </NativeSelect>
  );
}

/** DateTimePair is a labelled date and time, side by side. */
export function DateTimePair({
  id,
  label,
  date,
  time,
  onDate,
  onTime,
  disabled,
  error,
}: {
  id: string;
  label: string;
  date: string;
  time: string;
  onDate: (v: string) => void;
  onTime: (v: string) => void;
  disabled?: boolean;
  error?: string;
}) {
  return (
    <div className="mb-4 min-w-0" role="group" aria-labelledby={id + "-l"}>
      <Label id={id + "-l"} htmlFor={id}>
        {label}
      </Label>
      <div className="flex flex-wrap gap-2">
        <Input id={id} type="date" aria-label={label + " date"} value={date} disabled={disabled} onChange={(e) => onDate(e.target.value)} className="min-w-[10rem] flex-[3]" aria-invalid={!!error} />
        <Input type="time" aria-label={label + " time"} value={time} disabled={disabled} onChange={(e) => onTime(e.target.value)} className="min-w-[9rem] flex-[2]" />
      </div>
      {error && <FieldMessage tone="bad">{error}</FieldMessage>}
    </div>
  );
}

/** Counter is "12 / 160" beside a field's label. */
export const Counter = ({ n, max }: { n: number; max: number }) => (
  <span className="text-sm text-ink-soft tabular-nums" aria-hidden="true">
    {n} / {max}
  </span>
);
