// Why a screen is read only (the old ui.writeLockReason), and the one line
// that says so. Every organiser and judge screen uses these.
import { Notice } from "@kit/components/ui/notice";
import type { EventDetail } from "@/lib/client";

/** writeLock is why nothing on the screen can change, or "" when writes are fine (settings: the configuration screens' wording). */
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
