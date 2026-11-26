// Your events: the rows' plain words, from role and stage only (the two
// fields the events list really has; landing-your-events.js nextAction/menuItems).
import type { EventRow } from "@/lib/client";

export function nextAction(e: EventRow): string {
  if (e.taken_down) return e.role === "organiser" ? "Home" : "";
  if (e.role === "participant" && e.approval_status === "pending") return "Waiting for the organisers";
  if (e.role === "participant" && e.approval_status === "rejected") return "Not accepted";
  const words: Record<string, Record<string, string>> = {
    organiser: { draft: "Finish setting up", open: "Sign-up is open", building: "Teams are building", closed: "Set up judging", judging: "Judges are scoring", results: "Open event" },
    judge: { draft: "Not open yet", open: "Not open yet", building: "Not open yet", closed: "Judging starts soon", judging: "Score teams", results: "Open event", archived: "Open event", ended: "Open event" },
    participant: { draft: "Not open yet", open: "Open event", building: "Keep building", closed: "Entries closed", judging: "Judges are scoring", results: "Open event", archived: "Open event", ended: "Open event" },
  };
  return words[e.role]?.[e.stage] || (e.role === "organiser" ? "Home" : "Open");
}

/** leaveItem is the row menu's second item: Leave, Withdraw, or why neither can happen. */
export function leaveItem(e: EventRow): { label: string; disabled?: true } {
  if (e.taken_down) return { label: "Can't leave, this event was taken down", disabled: true };
  if (e.stage === "archived") return { label: "Can't leave, the event has ended", disabled: true };
  if (e.role === "participant" && e.approval_status === "pending") return { label: "Withdraw application…" };
  return { label: "Leave this event…" };
}
