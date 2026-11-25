import type { ColumnDef } from "@tanstack/react-table";
import { DataTable } from "@/components/ui/data-table";
import { EmptyState } from "@/components/ui/empty-state";
import { Section } from "@/components/ui/page-shell";
import { Stat, Stats } from "@/components/ui/stat";
import { bytes, dateOf, num } from "@/lib/format";
import { ownerName, siteLive, type AdminData, type Person, type Site } from "./data";
import { Breakable, SiteName, When } from "./bits";

const rank = (_: unknown, i: number) => i + 1;

export function OverviewTab({ d, go }: { d: AdminData; go: (tab: "users" | "sites-by-views") => void }) {
  const p = d.overview.people;
  const live = d.sites.filter(siteLive).length;
  const down = d.sites.filter((s) => s.taken_down).length;
  const susp = d.people.filter((u) => u.suspended).length;
  const disk = d.sites.reduce((a, s) => a + (s.bytes || 0), 0);
  const net = d.overview.network || [];
  const latest = [...d.people].sort((a, b) => a.n - b.n).slice(0, 10);
  const top = d.sites.filter((s) => s.views_30d > 0).sort((a, b) => b.views_30d - a.views_30d).slice(0, 10);
  const big = [...d.sites].sort((a, b) => b.bytes - a.bytes).slice(0, 10);

  const latestCols: ColumnDef<Person>[] = [
    { id: "joined", header: "Joined", cell: ({ row }) => <When at={row.original.created_at} />, meta: { phone: "meta" } },
    { id: "handle", header: "Handle", cell: ({ row }) => (row.original.handle ? <Breakable text={row.original.handle} /> : "-"), meta: { phone: "title" } },
    { id: "email", header: "Email", cell: ({ row }) => <Breakable text={row.original.email} />, meta: { phone: "meta" } },
  ];
  const viewCols: ColumnDef<Site>[] = [
    { id: "rank", header: "#", accessorFn: rank, meta: { align: "right", phone: "hide" } },
    { id: "site", header: "Website", cell: ({ row }) => <SiteName s={row.original} />, meta: { phone: "title" } },
    { id: "owner", header: "Owner", cell: ({ row }) => <Breakable text={ownerName(row.original)} /> },
    { id: "views", header: "Views", cell: ({ row }) => num(row.original.views_30d), meta: { align: "right" } },
  ];
  const bigCols: ColumnDef<Site>[] = [
    { id: "rank", header: "#", accessorFn: rank, meta: { align: "right", phone: "hide" } },
    { id: "site", header: "Website", cell: ({ row }) => <SiteName s={row.original} />, meta: { phone: "title" } },
    { id: "owner", header: "Owner", cell: ({ row }) => <Breakable text={ownerName(row.original)} /> },
    { id: "live", header: "Live", cell: ({ row }) => bytes(row.original.live_bytes), meta: { align: "right" } },
    { id: "disk", header: "On disk", cell: ({ row }) => bytes(row.original.bytes), meta: { align: "right" } },
  ];

  const month = d.overview.network_month;
  let netBlock = null;
  if (net.length || (month && (month.bytes_in || month.bytes_out))) {
    const today = net[net.length - 1];
    const out30 = net.reduce((a, x) => a + (x.bytes_out || 0), 0);
    const in30 = net.reduce((a, x) => a + (x.bytes_in || 0), 0);
    const since = month?.since || net[0]?.day;
    netBlock = (
      <Section
        title="Network"
        count={month ? monthName(month.month) + ", UTC" : "UTC"}
        note={"Everything the service sent and received, counted as requests pass" + (since ? ", since " + dateOf(since + "T12:00:00Z") : "") + "."}
      >
        <Stats>
          {month && <Stat value={bytes(month.bytes_out)} label="Out this month" />}
          {month && <Stat value={bytes(month.bytes_in)} label="In this month" />}
          <Stat value={bytes(out30)} label="Out, last 30 days" />
          <Stat value={bytes(in30)} label="In, last 30 days" />
          {today && <Stat value={bytes(today.bytes_out)} label={"Out on " + dateOf(today.day + "T12:00:00Z")} />}
        </Stats>
      </Section>
    );
  }

  return (
    <>
      <Section title="Key numbers" hideTitle>
        <Stats>
          <Stat value={num(p.total)} label="Users" />
          <Stat value={num(p.new_7d)} label="New in 7 days" />
          <Stat value={num(p.new_30d)} label="New in 30 days" />
          <Stat value={num(live)} label={"Live sites of " + num(d.sites.length)} />
          <Stat value={bytes(disk)} label="Site storage" />
          <Stat value={susp + " · " + down} label="Suspended · taken down" />
          {d.overview.disk && <Stat value={pct(d.overview.disk) + "%"} label={"Disk used · " + bytes(d.overview.disk.free_bytes) + " free"} />}
        </Stats>
        {d.overview.disk && <Disk disk={d.overview.disk} sites={disk} />}
      </Section>
      {netBlock}
      <div className="grid grid-cols-1 gap-x-8 lg:grid-cols-2">
        <Section title="Latest sign-ups" className="mt-7 first:mt-7">
          <DataTable label="Latest sign-ups" data={latest} columns={latestCols} getRowId={(u) => u.id} empty={<EmptyState title="Nobody yet" />} paged={false} sortable={false} />
          <a href="#users" onClick={(e) => (e.preventDefault(), go("users"))} className="inline-flex min-h-11 items-center text-sm">
            All users
          </a>
        </Section>
        <Section title="Most views" count="last 30 days" className="mt-7 first:mt-7">
          <DataTable label="Most views" data={top} columns={viewCols} getRowId={(s) => s.id} empty={<EmptyState title="No visits in 30 days" />} paged={false} sortable={false} />
          <a href="#sites" onClick={(e) => (e.preventDefault(), go("sites-by-views"))} className="inline-flex min-h-11 items-center text-sm">
            All sites by views
          </a>
        </Section>
      </div>
      <Section title="Biggest websites" note="Ranked by size on disk: the live copy plus every kept version.">
        <DataTable label="Biggest websites" data={big} columns={bigCols} getRowId={(s) => s.id} empty={<EmptyState title="No websites yet" />} paged={false} sortable={false} />
      </Section>
    </>
  );
}

const MONTHS = ["January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"];
const monthName = (ym: string) => MONTHS[Number(ym.slice(5, 7)) - 1] + " " + ym.slice(0, 4);

type DiskUse = { bytes: number; free_bytes: number; used_bytes: number };
const pct = (d: DiskUse) => (d.bytes ? Math.round((d.used_bytes / d.bytes) * 100) : 0);

/** Disk is how full the data disk is, with the old page's warnings at 75 and 90 percent. */
function Disk({ disk, sites }: { disk: DiskUse; sites: number }) {
  const p = pct(disk);
  const level = p >= 90 ? "full" : p >= 75 ? "filling" : "ok";
  const says =
    level === "full"
      ? `The disk is nearly full: ${p}% used, ${bytes(disk.free_bytes)} left. Publishing will start failing. Free space or move to a bigger disk now.`
      : level === "filling"
        ? `The disk is filling up: ${p}% used, ${bytes(disk.free_bytes)} left. Worth acting on before an event rather than during one.`
        : `${bytes(disk.used_bytes)} of ${bytes(disk.bytes)} used, ${bytes(disk.free_bytes)} free. Sites take ${bytes(sites)} of it.`;
  return (
    <div className="mt-3">
      <div
        role="meter"
        aria-label="Disk used"
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={p}
        aria-valuetext={p + "% used"}
        className="h-2.5 overflow-hidden rounded-tag bg-paper-lo"
      >
        <div className={level === "ok" ? "h-full bg-accent" : "h-full bg-bad"} style={{ width: Math.min(100, p) + "%" }} />
      </div>
      <p className={level === "ok" ? "mt-1.5 text-sm text-ink-soft" : "mt-1.5 flex items-start gap-2 text-sm font-medium text-bad"}>
        {level !== "ok" && <span aria-hidden="true">⚠</span>}
        {says}
      </p>
    </div>
  );
}
