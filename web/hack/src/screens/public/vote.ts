// The vote page's plain words (the old hack-voting.html inline script).
/** ineligible is why a signed-in visitor cannot vote. */
export function ineligible(eligibility?: string): string {
  if (eligibility === "participants") return "Only approved participants can vote in this event.";
  if (eligibility === "event_members") return "Only approved event members can vote in this event.";
  return "Your account is not eligible to vote in this event.";
}

/** intro is the vote page's line under its title. */
export function intro(status: string): string {
  if (status === "open") return "Choose one team. You can change your vote until voting closes.";
  if (status === "upcoming") return "Voting opens soon.";
  if (status === "closed") return "Voting has closed. Here are the final votes.";
  return "Voting isn’t open for this event.";
}
