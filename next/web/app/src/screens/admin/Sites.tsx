import type { ColumnDef, SortingState } from "@tanstack/react-table";
import { Chips } from "@/components/ui/badge";
import { DataTable } from "@/components/ui/data-table";
import { EmptyState } from "@/components/ui/empty-state";
import { Input } from "@/components/ui/input";
import { Menu, type MenuItem } from "@/components/ui/menu";
import { Section } from "@/components/ui/page-shell";
import { bytes, num, searchMatches } from "@/lib/format";
import { ACCESS, SITE_FILTERS, dashLink, ownerName, siteFilter, type AdminData, type Site, type SiteFilter } from "./data";
import { useActions } from "./actions";
import { SiteName, SiteStatus, When } from "./bits";

export type SitesState = { q: string; filter: SiteFilter; sorting: SortingState };
export const sitesStart: SitesState = { q: "", filter: "all", sorting: [{ id: "updated", desc: true }] };

function Address({ s }: { s: Site }) {
  if (!s.domain && !s.short_name) return <span className="text-ink-soft">-</span>;
  return (
    <>
      {s.domain && (
        <div>
          {s.domain}
          {s.domain_status && s.domain_status !== "live" && s.domain_status !== "active" && <span className="text-ink-soft"> · {s.domain_status}</span>}
        </div>
      )}
      {s.short_name && (
        <div>
          {s.short_name} <span className="text-ink-soft">· short name</span>
        </div>
      )}
    </>
  );
}

function SiteMenu({ s }: { s: Site }) {
  const act = useActions();
  const items: MenuItem[] = [];
  if (s.url) items.push({ label: "Open site", href: s.url, external: true });
  if (s.owner) items.push({ label: "Versions, data and settings", href: dashLink(s) });
  items.push({ separator: true });
  if (s.owner_suspended) items.push({ label: "Down with the account (re-enable it in Users)", disabled: true });
  else if (s.taken_down) items.push({ label: "Restore", onSelect: () => act.restore(s) });
  else items.push({ label: "Take down", onSelect: () => act.takeDown(s), danger: true });
  return <Menu label={"Actions for " + s.name} items={items} />;
}

const t = (s: string) => Date.parse(s) || 0;
const most = { sortDescFirst: true } as const;

const columns: ColumnDef<Site>[] = [
  { id: "name", header: "Site", accessorKey: "name", cell: ({ row }) => <SiteName s={row.original} />, meta: { phone: "title", wrap: true } },
  { id: "owner", header: "Owner", accessorFn: (s) => (s.owner || "").toLowerCase(), cell: ({ row }) => ownerName(row.original), meta: { wrap: true } },
  { id: "live", header: "Live", accessorKey: "live_bytes", cell: ({ getValue }) => bytes(getValue()), ...most, meta: { align: "right", phone: "hide", sortNames: ["Largest first", "Smallest first"] } },
  { id: "size", header: "Size", accessorKey: "bytes", cell: ({ getValue }) => bytes(getValue()), ...most, meta: { align: "right", sortNames: ["Largest first", "Smallest first"] } },
  {
    id: "updated",
    header: "Last updated",
    accessorFn: (s) => t(s.changed_at),
    cell: ({ row }) => <When at={row.original.changed_at} />,
    ...most,
    meta: { phone: "hide", sortNames: ["Newest first", "Oldest first"] },
  },
  { id: "views", header: "Views, 30 days", accessorKey: "views_30d", cell: ({ getValue }) => num(getValue()), ...most, meta: { align: "right", sortNames: ["Most first", "Fewest first"] } },
  { id: "people", header: "People", accessorKey: "people_30d", cell: ({ getValue }) => num(getValue()), ...most, meta: { align: "right", phone: "hide", sortNames: ["Most first", "Fewest first"] } },
  {
    id: "created",
    header: "Created",
    accessorFn: (s) => t(s.created_at),
    ...most,
    meta: { hidden: true, sortNames: ["Newest first", "Oldest first"] },
  },
  {
    id: "access",
    header: "Who can open",
    enableSorting: false,
    cell: ({ row }) => (
      <>
        {ACCESS[row.original.access] || row.original.access}
        {!row.original.listed && <span className="text-ink-soft"> · not on profile</span>}
      </>
    ),
  },
  { id: "status", header: "Status", enableSorting: false, cell: ({ row }) => <SiteStatus s={row.original} />, meta: { phone: "end" } },
  { id: "address", header: "Own address", enableSorting: false, cell: ({ row }) => <Address s={row.original} />, meta: { phone: "hide", wrap: true } },
  { id: "menu", header: () => <span className="sr-only">Actions</span>, enableSorting: false, cell: ({ row }) => <SiteMenu s={row.original} />, meta: { phone: "end" } },
];

export function SitesTab({ d, st, set }: { d: AdminData; st: SitesState; set: (s: SitesState) => void }) {
  const rows = d.sites.filter(
    (s) => siteFilter(st.filter, s) && searchMatches(st.q, s.name + " " + (s.owner || "") + " " + (s.domain || "") + " " + (s.short_name || "")),
  );
  return (
    <Section title="Sites" count={"(" + (rows.length !== d.sites.length ? rows.length + " of " : "") + d.sites.length + ")"}>
      <Input
        type="search"
        className="mt-2"
        value={st.q}
        onChange={(e) => set({ ...st, q: e.target.value })}
        placeholder="Site, owner or domain (-word hides)"
        aria-label="Search sites; start a word with a minus sign to hide matches"
        autoComplete="off"
        spellCheck={false}
      />
      <Chips label="Filter sites" options={SITE_FILTERS} value={st.filter} onChange={(filter) => set({ ...st, filter })} />
      <DataTable
        label="Sites"
        data={rows}
        columns={columns}
        getRowId={(s) => s.id}
        sorting={st.sorting}
        onSortingChange={(sorting) => set({ ...st, sorting })}
        empty={<EmptyState title="No site matches">Try fewer words, or the All filter.</EmptyState>}
      />
    </Section>
  );
}
