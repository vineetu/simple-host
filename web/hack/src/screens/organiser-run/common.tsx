// What every organiser run screen shares: why writes are off (a platform
// admin's read-only look, a taken-down or ended event), the error line for a
// refused or failed call, downloads that need the key, and the frame each
// screen sits in (with the Ask panel).
import { useCallback, type ReactNode } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Notice } from "@kit/components/ui/notice";
import { useToast } from "@kit/components/ui/toast";
import { ApiError } from "@kit/api/call";
import type { EventDetail } from "@/lib/client";
import { useBytes } from "@/app/Providers";
import { Assistant } from "./Assistant";

/** writeLock is why nothing on the screen can change (ui.writeLockReason), or "" when writes are fine. */
export function writeLock(d: Pick<EventDetail, "event" | "admin_view"> | undefined, opts: { settings?: boolean } = {}): string {
  if (!d) return "";
  if (d.admin_view) return "You are viewing as a platform admin. Nothing on this page can be changed.";
  if (d.event.taken_down) return "This event has been taken down. Nothing on this page can be changed.";
  if (d.event.stage === "archived") return opts.settings ? "This event has ended. Settings can't change any more." : "This event has ended. Nothing on this page changes any more.";
  return "";
}

/** LockNote is the one line that says why the screen is read only. */
export function LockNote({ reason }: { reason: string }) {
  if (!reason) return null;
  return <Notice title={reason} />;
}

/** errText is the line to show for a call that failed (the server's words, or that it could not be reached). */
export function errText(e: unknown): string {
  if (e instanceof ApiError) return e.status === 0 ? "Cannot reach the server." : e.message;
  return "Cannot reach the server.";
}

/** useDownload saves a file the key may read (an export) under a name; it says so in a toast either way. */
export function useDownload() {
  const bytes = useBytes();
  const toast = useToast();
  return useCallback(
    async (path: string, filename: string) => {
      let blob: Blob;
      try {
        blob = await bytes(path);
      } catch (e) {
        if (e instanceof ApiError && e.status === 401) return false;
        toast(e instanceof ApiError && e.status !== 0 ? e.message || "Could not download the file." : "Cannot reach the server.", { bad: true });
        return false;
      }
      try {
        const url = URL.createObjectURL(blob);
        const a = document.createElement("a");
        a.href = url;
        a.download = filename;
        document.body.append(a);
        a.click();
        a.remove();
        setTimeout(() => URL.revokeObjectURL(url), 1000);
      } catch {
        /* the catalogue has no object URLs to give; the toast still says it */
      }
      toast("Downloaded " + filename);
      return true;
    },
    [bytes, toast],
  );
}

/** useRefreshEvent re-reads GET E (the frame, badges and next step) and anything else named. */
export function useRefreshEvent(slug: string) {
  const qc = useQueryClient();
  return useCallback(
    (...more: unknown[][]) => Promise.all([qc.invalidateQueries({ queryKey: ["event", slug] }), ...more.map((k) => qc.invalidateQueries({ queryKey: k }))]),
    [qc, slug],
  );
}

/** RunFrame is every organiser run screen's body, with the Ask panel beside it. */
export function RunFrame({ slug, screen, children }: { slug: string; screen: string; children: ReactNode }) {
  return (
    <>
      {children}
      <Assistant slug={slug} screen={screen} />
    </>
  );
}
