import type { ColumnDef } from "@tanstack/react-table";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { DataTable } from "@/components/ui/data-table";
import { EmptyState } from "@/components/ui/empty-state";
import { Section } from "@/components/ui/page-shell";
import { plural } from "@/lib/format";
import { dashLink, ownerName, type AdminData, type Person, type Report, type Site } from "./data";
import { useActions } from "./actions";
import { When } from "./bits";

type ReportRow = Report & { s?: Site };
type DownRow = { id: string; user?: Person; site?: Site };

export function ModerationTab({ d }: { d: AdminData }) {
  const act = useActions();
  const reports: ReportRow[] = (d.overview.reports || []).map((r) => ({
    ...r,
    s: d.byHandle[r.owner]?.siteList.find((x) => x.name === r.site),
  }));
  const down: DownRow[] = [
    ...d.people.filter((u) => u.suspended).map((user) => ({ id: "u" + user.id, user })),
    ...d.sites.filter((s) => s.taken_down && !s.owner_suspended).map((site) => ({ id: "s" + site.id, site })),
  ];

  const reportCols: ColumnDef<ReportRow>[] = [
    {
      id: "page",
      header: "Page",
      cell: ({ row: { original: r } }) => (
        <>
          <a href={/^https?:\/\//i.test(r.url) ? r.url : "#"} target="_blank" rel="noopener">
            {r.url}
          </a>
          {r.s && (
            <div className="text-xs font-normal text-ink-soft">
              {r.s.name} of {r.s.owner}
            </div>
          )}
        </>
      ),
      meta: { phone: "title", wrap: true },
    },
    { id: "reason", header: "Reason", accessorKey: "reason", meta: { wrap: true } },
    { id: "when", header: "Reported", cell: ({ row }) => <When at={row.original.created_at} /> },
    {
      id: "act",
      header: () => <span className="sr-only">Actions</span>,
      cell: ({ row: { original: r } }) =>
        r.s && (
          <div className="flex flex-wrap items-center gap-2">
            {r.s.taken_down ? (
              <Badge tone="down">taken down</Badge>
            ) : (
              <Button variant="danger" onClick={() => act.takeDown(r.s!)}>
                Take down
              </Button>
            )}
            <Button asChild>
              <a href={dashLink(r.s)}>Open</a>
            </Button>
          </div>
        ),
      meta: { phone: "foot" },
    },
  ];
  const downCols: ColumnDef<DownRow>[] = [
    {
      id: "what",
      header: "Site or account",
      cell: ({ row: { original: x } }) =>
        x.user ? (
          <>
            <strong>{x.user.handle || x.user.email}</strong> <Badge tone="down">account suspended</Badge>
            <div className="text-xs font-normal text-ink-soft">{plural(x.user.sites, "site")} down with it</div>
          </>
        ) : (
          <>
            <strong>{x.site!.name}</strong> <Badge tone="down">taken down</Badge>
          </>
        ),
      meta: { phone: "title" },
    },
    { id: "owner", header: "Owner", cell: ({ row: { original: x } }) => (x.user ? x.user.email : ownerName(x.site!)) },
    { id: "reason", header: "Reason", cell: ({ row: { original: x } }) => (x.user ? x.user.suspended_reason : x.site!.taken_down_reason) || "-", meta: { wrap: true } },
    {
      id: "act",
      header: () => <span className="sr-only">Actions</span>,
      cell: ({ row: { original: x } }) =>
        x.user ? <Button onClick={() => act.enable(x.user!)}>Re-enable</Button> : <Button onClick={() => act.restore(x.site!)}>Restore</Button>,
      meta: { phone: "foot" },
    },
  ];
  return (
    <>
      <Section title="Reports" count={"(" + reports.length + ")"} note="Pages visitors reported. Taking the site down closes its reports.">
        <DataTable label="Reports" data={reports} columns={reportCols} getRowId={(r) => String(r.id)} empty={<EmptyState title="No open reports" />} sortable={false} />
      </Section>
      <Section title="Taken down" count={"(" + down.length + ")"} note="Sites taken down and suspended accounts. Nothing is deleted; Restore and Re-enable put them back.">
        <DataTable label="Taken down" data={down} columns={downCols} getRowId={(x) => x.id} empty={<EmptyState title="Nothing is taken down" />} sortable={false} />
      </Section>
    </>
  );
}
