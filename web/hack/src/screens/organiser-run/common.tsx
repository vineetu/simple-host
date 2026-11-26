// What every organiser run screen shares: why writes are off (a platform
// admin's read-only look, a taken-down or ended event), the error line for a
// refused or failed call, downloads that need the key, and the frame each
// screen sits in (with the Ask panel).
import { useCallback } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useToast } from "@kit/components/ui/toast";
import { ApiError } from "@kit/api/call";
import { useBytes } from "@/app/Providers";

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
