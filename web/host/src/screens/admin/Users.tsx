import type { ColumnDef, SortingState } from "@tanstack/react-table";
import { Badge, Chips } from "@kit/components/ui/badge";
import { Button } from "@kit/components/ui/button";
import { DataTable } from "@kit/components/ui/data-table";
import { EmptyState } from "@kit/components/ui/empty-state";
import { Input } from "@kit/components/ui/input";
import { Section } from "@kit/components/ui/page-shell";
import { bytes, dateOf, num, plural, searchMatches } from "@kit/lib/format";
import { USER_FILTERS, userFilter, type AdminData, type Person, type UserFilter } from "./data";
import { useActions } from "./actions";
import { Breakable, UserStatus, When } from "./bits";

export type UsersState = { q: string; filter: UserFilter; sorting: SortingState };
export const usersStart: UsersState = { q: "", filter: "all", sorting: [{ id: "joined", desc: true }] };

const time = (s: string | null) => (s ? Date.parse(s) : 0);

const columns: ColumnDef<Person>[] = [
  { id: "n", header: "#", accessorKey: "n", enableSorting: false, meta: { align: "right", phone: "hide" } },
  {
    id: "handle",
    header: "Handle",
    accessorFn: (u) => (u.handle || u.email).toLowerCase(),
    cell: ({ row }) => (row.original.handle ? <Breakable text={row.original.handle} /> : <span className="text-ink-soft">no handle</span>),
    meta: { phone: "title", wrap: true },
  },
  { id: "email", header: "Email", accessorKey: "email", enableSorting: false, cell: ({ getValue }) => <Breakable text={getValue() as string} />, meta: { wrap: true } },
  {
    id: "joined",
    header: "Joined",
    accessorFn: (u) => time(u.created_at),
    cell: ({ row }) => <When at={row.original.created_at} />,
    sortDescFirst: true,
    meta: { sortNames: ["Newest first", "Oldest first"] },
  },
  { id: "sites", header: "Sites", accessorKey: "sites", cell: ({ getValue }) => num(getValue()), sortDescFirst: true, meta: { align: "right", sortNames: ["Most first", "Fewest first"] } },
  { id: "size", header: "Size", accessorKey: "bytes", cell: ({ getValue }) => bytes(getValue()), sortDescFirst: true, meta: { align: "right", sortNames: ["Largest first", "Smallest first"] } },
  { id: "views", header: "Views, 30 days", accessorKey: "views_30d", cell: ({ getValue }) => num(getValue()), sortDescFirst: true, meta: { align: "right", phone: "hide", sortNames: ["Most first", "Fewest first"] } },
  {
    id: "seen",
    header: "Last seen",
    accessorFn: (u) => time(u.last_seen),
    cell: ({ row }) => <When at={row.original.last_seen} never="never" />,
    sortDescFirst: true,
    meta: { phone: "hide", sortNames: ["Most recent first", "Longest ago first"] },
  },
  { id: "status", header: "Status", cell: ({ row }) => <UserStatus u={row.original} />, enableSorting: false, meta: { phone: "end" } },
];

export function UsersTab({ d, st, set, openSites }: { d: AdminData; st: UsersState; set: (s: UsersState) => void; openSites: (handle: string) => void }) {
  const rows = d.people.filter((u) => userFilter(st.filter, u) && searchMatches(st.q, u.handle + " " + u.email + " " + (u.display_name || "")));
  return (
    <Section title="Users" count={"(" + (rows.length !== d.people.length ? rows.length + " of " : "") + d.people.length + ")"}>
      <Input
        type="search"
        className="mt-2"
        value={st.q}
        onChange={(e) => set({ ...st, q: e.target.value })}
        placeholder="Search handle or email (-word hides)"
        aria-label="Search users by handle or email; start a word with a minus sign to hide matches"
        autoComplete="off"
        spellCheck={false}
      />
      <Chips label="Filter users" options={USER_FILTERS} value={st.filter} onChange={(filter) => set({ ...st, filter })} />
      <DataTable
        label="Users"
        data={rows}
        columns={columns}
        getRowId={(u) => u.id}
        sorting={st.sorting}
        onSortingChange={(sorting) => set({ ...st, sorting })}
        empty={<EmptyState title="No one matches">Try fewer words, or the All filter.</EmptyState>}
        detail={(u) => <UserPanel u={u} openSites={openSites} />}
      />
    </Section>
  );
}

function UserPanel({ u, openSites }: { u: Person; openSites: (handle: string) => void }) {
  const act = useActions();
  return (
    <div className="text-[15px]">
      <p className="mb-1.5 text-sm text-ink-soft">
        {u.email}
        {u.display_name ? " · " + u.display_name : ""} · joined {dateOf(u.created_at)} · {plural(u.sites, "site")}
      </p>
      {u.suspended && (
        <p className="mb-1.5 text-sm text-bad">
          Suspended: {u.suspended_reason || "no reason given"}. Sign-in and keys are refused; every site of theirs is taken down.
        </p>
      )}
      {u.siteList.length ? (
        <ul className="m-0 grid list-none grid-cols-[repeat(auto-fill,minmax(220px,1fr))] gap-x-5 gap-y-1.5 p-0">
          {u.siteList.map((s) => (
            <li key={s.id} className="min-w-0">
              <strong>{s.name}</strong>{" "}
              {s.taken_down ? <Badge tone="down">taken down</Badge> : s.offline ? <Badge>offline</Badge> : !s.live_version ? <Badge>not live</Badge> : null}
              {s.url && (
                <>
                  <br />
                  <a href={s.url} target="_blank" rel="noopener">
                    {s.url.replace(/^https?:\/\//, "").replace(/\/$/, "")}
                  </a>
                </>
              )}
              {s.domain && (
                <>
                  <br />
                  <span className="text-ink-soft">{s.domain}</span>
                </>
              )}
            </li>
          ))}
        </ul>
      ) : (
        <p className="text-ink-soft">No sites yet.</p>
      )}
      {!u.operator && (
        <div className="mt-2.5 flex flex-wrap gap-2">
          {u.suspended ? (
            <Button onClick={() => act.enable(u)}>Re-enable</Button>
          ) : (
            <Button variant="danger" onClick={() => act.suspend(u)}>
              Suspend
            </Button>
          )}
          {u.handle && <Button onClick={() => openSites(u.handle)}>Open in Sites</Button>}
        </div>
      )}
    </div>
  );
}
