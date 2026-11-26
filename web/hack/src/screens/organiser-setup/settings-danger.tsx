// Settings → Leave or delete: leaving (the shared leave dialog: hand over,
// co-organiser, or the only organiser closing the event) above deleting the
// event for good. Deleting an event anyone joined, or one that has ended,
// asks for its address name typed.
import { useState, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { Button } from "@kit/components/ui/button";
import { useHack } from "@/lib/client";
import { flash } from "@/lib/session";
import { useLeave } from "@/app/Leave";
import { useGo } from "@/app/nav";
import type { TabProps } from "./Settings";
import { errorText, LockNote, Panel } from "./common";
import { locks, TypedConfirm, useAnchor } from "./settings-ui";

function Row({ id, title, text, children }: { id: string; title: string; text: string; children: ReactNode }) {
  return (
    <div id={id} className="flex flex-wrap items-center gap-x-4 gap-y-2 border-b border-rule py-3 last:border-b-0">
      <div className="min-w-0 flex-[1_1_16rem]">
        <p className="text-ink">{title}</p>
        <p className="text-sm text-ink-soft">{text}</p>
      </div>
      {children}
    </div>
  );
}

export function DangerTab({ d }: TabProps) {
  const api = useHack();
  const go = useGo();
  const ev = d.event;
  const slug = ev.slug;
  const { admin } = locks(d);
  const [openLeave, leaveDialog] = useLeave();
  const [deleting, setDeleting] = useState(false);
  const preview = useQuery({ queryKey: ["leave", slug], queryFn: () => api.event.leavePreview(slug), retry: false });
  useAnchor(!preview.isPending);
  const counts = ((d.organiser as { counts?: { participants?: number; judges?: number } } | undefined)?.counts || {}) as { participants?: number; judges?: number };
  const typed = ev.stage === "archived" || (counts.participants || 0) + (counts.judges || 0) > 0;
  const p = preview.data;
  let leaveText = "";
  let leaveOff = !!admin;
  if (ev.stage === "archived") {
    leaveOff = true;
    leaveText = "This event has ended, so nobody can leave it. It stays on your Home as a record.";
  } else if (ev.taken_down) {
    leaveOff = true;
    leaveText = "This event has been taken down.";
  } else if (p?.case === "creator") leaveText = "You created this event, so you’ll hand it to another organiser before you leave.";
  else if (p?.case === "last_organiser") leaveText = "You’re the only organiser, so leaving closes the event. You’ll choose to end it or delete it.";
  else leaveText = "You’ll lose access to managing this event. The other organisers keep running it.";
  return (
    <>
      <LockNote text={admin} />
      <Panel title="Leave or delete" danger>
        {/* No Leave row when its preview could not be read: never offer a Leave we can't vouch for. */}
        {p && (
          <Row id="leave" title="Leave this event" text={leaveText}>
            <Button variant="danger" disabled={leaveOff} onClick={() => openLeave(slug, ev.title)}>
              Leave event
            </Button>
          </Row>
        )}
        <Row id="delete" title="Delete this event" text="Removes the public page, results, team sites, entries, scores, votes and memberships for good. There is no undo.">
          <Button variant="danger" disabled={!!admin} onClick={() => setDeleting(true)}>
            Delete event
          </Button>
        </Row>
      </Panel>
      {leaveDialog}
      <TypedConfirm
        open={deleting}
        onOpenChange={setDeleting}
        title={"Delete " + ev.title + "?"}
        body="The public page, results, team sites, entries, scores, votes and memberships go for good. This cannot be undone."
        match={typed ? slug : ""}
        confirmLabel="Delete event"
        onConfirm={async () => {
          try {
            await api.event.remove(slug, typed ? { confirm: slug } : {});
          } catch (e) {
            throw new Error(errorText(e));
          }
          flash("Event deleted.");
          go("/events");
        }}
      />
    </>
  );
}
