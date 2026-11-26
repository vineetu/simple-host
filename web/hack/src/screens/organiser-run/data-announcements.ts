// Announcements: the organiser's posts (GET/POST E/announcements, PUT/DELETE
// E/announcements/{id}, POST E/announcements/preview), as organiser-announcements.js reads them.

export type Announcement = { id: string; title: string; body: string; created_at?: string; pinned?: boolean; emails_sent?: number };
export type Posted = { emails_queued?: number; organiser_copied?: boolean };

/** summary is the body as plain text for the list: [label](url) is its label, ** goes. */
export const summary = (text: string) => text.replace(/\[([^\]]+)\]\(https?:\/\/(?:[^()\s]|\([^()\s]*\))+\)/gi, "$1").replace(/\*\*/g, "");

/** postedText is the result line after posting, from what the server says it queued. */
export function postedText(emailed: boolean, r: Posted): { text: string; bad?: boolean } {
  if (!emailed) return { text: "Posted" };
  const queued = r.emails_queued || 0;
  if (queued === 0) return { text: "Posted. Nobody had an email address on file, so no email was sent.", bad: true };
  return { text: `Posted and emailed to ${queued} ${queued === 1 ? "person" : "people"}${r.organiser_copied ? " (including a copy to you)" : ""}.` };
}

/** linkFor checks an address for Add link and gives the markdown to insert, or the error. */
export function linkFor(rawAddress: string, rawLabel: string): { text: string } | { error: string } {
  let address = rawAddress.trim();
  if (!/^[a-z][a-z0-9+.-]*:/i.test(address) && !address.startsWith("//")) address = "https://" + address;
  const bad = { error: "Use an http or https address." };
  try {
    const url = new URL(address);
    if (!["http:", "https:"].includes(url.protocol) || !/^https?:\/\//i.test(address) || !url.hostname || url.username || url.password || /[\s\p{Cc}\p{Cf}]/u.test(address)) return bad;
    address = url.href.replace(/\(/g, "%28").replace(/\)/g, "%29");
    if (url.hostname.length > 253 || !/^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$/i.test(url.hostname) || address.length > 2000) return bad;
  } catch {
    return bad;
  }
  const label = rawLabel.trim();
  if (/[[\]\n]/.test(label)) return { error: "Use a label without brackets or line breaks." };
  return { text: label ? `[${label}](${address})` : address };
}
