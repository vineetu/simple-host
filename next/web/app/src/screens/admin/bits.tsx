// Small cells the admin lists share.
import { Badge } from "@/components/ui/badge";
import { ago, dateOf } from "@/lib/format";
import type { Person, Site } from "./data";

/** Breakable is a long name or address with line-break chances after its
 * dots, dashes and @, so it wraps between parts instead of mid-word. */
export function Breakable({ text }: { text: string }) {
  return <>{text.split(/(?<=[.\-@])/).flatMap((part, i) => (i ? [<wbr key={i} />, part] : [part]))}</>;
}

/** When is "25 Nov 2026 · today"; the "· today" part drops on narrow screens. */
export function When({ at, never = "-" }: { at: string | null; never?: string }) {
  if (!at) return <span className="text-ink-soft">{never}</span>;
  return (
    <>
      <span className="whitespace-nowrap">{dateOf(at)}</span> <span className="whitespace-nowrap text-ink-soft">· {ago(at)}</span>
    </>
  );
}

export function SiteName({ s }: { s: Pick<Site, "name" | "url"> }) {
  if (!s.url) return <Breakable text={s.name} />;
  return (
    <a href={/^https?:\/\//i.test(s.url) ? s.url : "#"} target="_blank" rel="noopener">
      <Breakable text={s.name} />
    </a>
  );
}

export function SiteStatus({ s }: { s: Site }) {
  if (s.owner_suspended) return <Badge tone="down">suspended</Badge>;
  if (s.taken_down) return <Badge tone="down">taken down</Badge>;
  if (s.offline) return <Badge>offline</Badge>;
  if (!s.live_version) return <Badge>not live</Badge>;
  return <Badge tone="ok">live</Badge>;
}

export function UserStatus({ u }: { u: Person }) {
  if (u.operator) return <Badge tone="accent">admin</Badge>;
  if (u.suspended) return <Badge tone="down">suspended</Badge>;
  return <Badge tone="ok">active</Badge>;
}
