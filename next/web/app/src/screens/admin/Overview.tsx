import type { ColumnDef } from "@tanstack/react-table";
import { DataTable } from "@/components/ui/data-table";
import { EmptyState } from "@/components/ui/empty-state";
import { Section } from "@/components/ui/page-shell";
import { Stat, Stats } from "@/components/ui/stat";
import { bytes, dateOf, num } from "@/lib/format";
import { ownerName, siteLive, type AdminData, type Person, type Site } from "./data";
import { SiteName, When } from "./bits";

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
    { id: "handle", header: "Handle", cell: ({ row }) => row.original.handle || "-", meta: { phone: "title" } },
    { id: "email", header: "Email", cell: ({ row }) => row.original.email, meta: { phone: "meta" } },
  ];
  const viewCols: ColumnDef<Site>[] = [
    { id: "rank", header: "#", accessorFn: rank, meta: { align: "right", phone: "hide" } },
    { id: "site", header: "Website", cell: ({ row }) => <SiteName s={row.original} />, meta: { phone: "title" } },
    { id: "owner", header: "Owner", cell: ({ row }) => ownerName(row.original) },
    { id: "views", header: "Views", cell: ({ row }) => num(row.original.views_30d), meta: { align: "right" } },
  ];
  const bigCols: ColumnDef<Site>[] = [
    { id: "rank", header: "#", accessorFn: rank, meta: { align: "right", phone: "hide" } },
    { id: "site", header: "Website", cell: ({ row }) => <SiteName s={row.original} />, meta: { phone: "title" } },
    { id: "owner", header: "Owner", cell: ({ row }) => ownerName(row.original) },
    { id: "live", header: "Live", cell: ({ row }) => bytes(row.original.live_bytes), meta: { align: "right" } },
    { id: "disk", header: "On disk", cell: ({ row }) => bytes(row.original.bytes), meta: { align: "right" } },
  ];

  let netBlock = null;
  if (net.length) {
    const today = net[net.length - 1];
    const tin = net.reduce((a, x) => a + (x.bytes_in || 0), 0);
    const tout = net.reduce((a, x) => a + (x.bytes_out || 0), 0);
    netBlock = (
      <Section
        title="Network"
        count="last 30 days, UTC"
        note={"Everything the service sent and received, counted as requests pass, since " + dateOf(net[0].day + "T12:00:00Z") + "."}
      >
        <Stats>
          <Stat value={bytes(tout)} label="Out" />
          <Stat value={bytes(tin)} label="In" />
          <Stat value={bytes(today.bytes_out)} label={"Out on " + dateOf(today.day + "T12:00:00Z")} />
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
        </Stats>
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
