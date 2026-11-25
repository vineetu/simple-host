import { Fragment, useId, useState, type ReactNode } from "react";
import {
  flexRender,
  getCoreRowModel,
  getPaginationRowModel,
  getSortedRowModel,
  useReactTable,
  type ColumnDef,
  type Row,
  type SortingState,
} from "@tanstack/react-table";
import { cn } from "@/lib/utils";
import { Button } from "./button";
import { NativeSelect } from "./input";

/**
 * How a column shows on a phone, where each row becomes a card:
 *   title  the card's heading (first line)
 *   meta   a "label value" pair on the line under it
 *   end    top right of the card (status, row menu)
 *   foot   under the card's lines, with no label (row buttons)
 *   hide   left out
 */
export type ColumnLook = {
  align?: "right";
  phone?: "title" | "meta" | "end" | "foot" | "hide";
  wrap?: boolean;
  /** Not shown at all; the column is only there to sort by. */
  hidden?: boolean;
  /** For the phone's sort list: [first, second] direction names, e.g. ["Oldest first", "Newest first"]. */
  sortNames?: [string, string];
};

declare module "@tanstack/react-table" {
  // eslint-disable-next-line @typescript-eslint/no-unused-vars, @typescript-eslint/no-empty-object-type
  interface ColumnMeta<TData, TValue> extends ColumnLook {}
}

export type { ColumnDef, SortingState };

const PAGE = 50;

/**
 * DataTable is a sortable list: a table on a desktop, one card per row on a
 * phone, 50 rows a page. Searching and filtering happen before data comes in.
 * detail (optional) is a panel a row opens.
 */
export function DataTable<T>({
  label,
  data,
  columns,
  getRowId,
  sorting,
  onSortingChange,
  empty,
  detail,
  pageSize = PAGE,
  paged = true,
  sortable = true,
}: {
  label: string;
  data: T[];
  columns: ColumnDef<T, any>[]; // eslint-disable-line @typescript-eslint/no-explicit-any
  getRowId: (row: T) => string;
  sorting?: SortingState;
  onSortingChange?: (s: SortingState) => void;
  empty: ReactNode;
  detail?: (row: T) => ReactNode;
  pageSize?: number;
  /** false: a short list with no pager line. */
  paged?: boolean;
  /** false: a fixed list (the overview's top tens). */
  sortable?: boolean;
}) {
  const [ownSorting, setOwnSorting] = useState<SortingState>([]);
  const [open, setOpen] = useState<Record<string, boolean>>({});
  const sortId = useId();
  const sort = sorting ?? ownSorting;
  const table = useReactTable({
    data,
    columns,
    getRowId,
    state: { sorting: sort },
    onSortingChange: (u) => {
      const next = typeof u === "function" ? u(sort) : u;
      (onSortingChange ?? setOwnSorting)(next);
    },
    getCoreRowModel: getCoreRowModel(),
    getSortedRowModel: getSortedRowModel(),
    getPaginationRowModel: getPaginationRowModel(),
    enableSorting: sortable,
    initialState: {
      pagination: { pageSize, pageIndex: 0 },
      columnVisibility: Object.fromEntries(columns.filter((c) => c.meta?.hidden).map((c) => [c.id, false])),
    },
    autoResetPageIndex: true,
  });
  const rows = table.getRowModel().rows;
  const leaf = table.getVisibleLeafColumns();
  const toggle = (id: string) => setOpen((o) => ({ ...o, [id]: !o[id] }));

  const cell = (row: Row<T>, id: string) => {
    const c = row.getVisibleCells().find((x) => x.column.id === id)!;
    return flexRender(c.column.columnDef.cell, c.getContext());
  };
  const headerText = (id: string) => {
    const h = table.getColumn(id)!.columnDef.header;
    return typeof h === "string" ? h : id;
  };

  if (!data.length) return <>{empty}</>;

  const sortCols = table.getAllLeafColumns().filter((c) => c.getCanSort());
  const current = sort[0];
  return (
    <div className="my-2">
      {/* The sort as a list too: a phone has no column heads to press. */}
      {sortCols.length > 0 && (
        <div className="mb-2 flex items-center gap-2 md:max-w-card">
          <label htmlFor={sortId} className="font-note text-note text-ink-soft">
            Sort
          </label>
          <NativeSelect
            id={sortId}
            className="flex-1"
            value={current ? current.id + ":" + (current.desc ? "desc" : "asc") : ""}
            onChange={(e) => {
              const [id, dir] = e.target.value.split(":");
              table.setSorting([{ id, desc: dir === "desc" }]);
            }}
          >
            {!current && <option value="">As listed</option>}
            {sortCols.flatMap((c) => {
              const names = c.columnDef.meta?.sortNames ?? ["A to Z", "Z to A"];
              const first = c.columnDef.sortDescFirst ? "desc" : "asc";
              const second = first === "desc" ? "asc" : "desc";
              return [
                <option key={c.id + first} value={c.id + ":" + first}>
                  {headerText(c.id)}: {names[0]}
                </option>,
                <option key={c.id + second} value={c.id + ":" + second}>
                  {headerText(c.id)}: {names[1]}
                </option>,
              ];
            })}
          </NativeSelect>
        </div>
      )}

      {/* Desktop: a table. */}
      <div className="hidden md:block">
        <table className="w-full border-collapse text-[15px] tabular-nums" aria-label={label}>
          <thead>
            {table.getHeaderGroups().map((hg) => (
              <tr key={hg.id}>
                {detail && <th className="w-11 bg-paper-lo" aria-label="Details" />}
                {hg.headers.map((h) => {
                  const meta = h.column.columnDef.meta;
                  const dir = h.column.getIsSorted();
                  return (
                    <th
                      key={h.id}
                      aria-sort={dir === "asc" ? "ascending" : dir === "desc" ? "descending" : undefined}
                      className={cn(
                        "border-b border-rule bg-paper-lo px-2 py-1 text-left font-note text-sm font-normal whitespace-nowrap text-ink-soft first:pl-3",
                        meta?.align === "right" && "text-right",
                      )}
                    >
                      {h.column.getCanSort() ? (
                        <button
                          type="button"
                          onClick={h.column.getToggleSortingHandler()}
                          className={cn(
                            "inline-flex min-h-11 min-w-11 cursor-pointer items-center gap-1 whitespace-nowrap",
                            dir ? "text-ink" : "text-ink-soft hover:text-ink",
                          )}
                        >
                          {flexRender(h.column.columnDef.header, h.getContext())}
                          <span aria-hidden="true" className="inline-block w-3">
                            {dir === "asc" ? "↑" : dir === "desc" ? "↓" : ""}
                          </span>
                        </button>
                      ) : (
                        flexRender(h.column.columnDef.header, h.getContext())
                      )}
                    </th>
                  );
                })}
              </tr>
            ))}
          </thead>
          <tbody>
            {rows.map((row) => (
              <Fragment key={row.id}>
                <tr className={cn("hover:bg-paper-lo/50", open[row.id] && "bg-paper-lo/50")}>
                  {detail && (
                    <td className="border-b border-rule py-0 align-top">
                      <DetailToggle open={!!open[row.id]} onClick={() => toggle(row.id)} />
                    </td>
                  )}
                  {row.getVisibleCells().map((c) => {
                    const meta = c.column.columnDef.meta;
                    return (
                      <td
                        key={c.id}
                        className={cn(
                          "border-b border-rule px-2 py-2 align-top first:pl-3",
                          meta?.align === "right" && "text-right",
                          meta?.align === "right" ? "whitespace-nowrap" : meta?.wrap ? "max-w-64 min-w-28 [overflow-wrap:anywhere]" : "[overflow-wrap:normal]",
                        )}
                      >
                        {flexRender(c.column.columnDef.cell, c.getContext())}
                      </td>
                    );
                  })}
                </tr>
                {detail && open[row.id] && (
                  <tr className="bg-paper-lo/50">
                    <td colSpan={leaf.length + 1} className="border-b border-rule px-3 py-3">
                      {detail(row.original)}
                    </td>
                  </tr>
                )}
              </Fragment>
            ))}
          </tbody>
        </table>
      </div>

      {/* Phone: one card per row. */}
      <ul className="m-0 list-none border-t border-rule p-0 md:hidden" aria-label={label}>
        {rows.map((row) => {
          const look = (where: ColumnLook["phone"]) => leaf.filter((c) => (c.columnDef.meta?.phone ?? "meta") === where);
          return (
            <li key={row.id} className="border-b border-rule py-2.5">
              <div className="flex items-start gap-2">
                <div className="min-w-0 flex-1">
                  <div className="font-semibold">
                    {look("title").map((c, i) => (
                      <Fragment key={c.id}>
                        {i > 0 && " "}
                        {cell(row, c.id)}
                      </Fragment>
                    ))}
                  </div>
                  <div className="mt-0.5 flex flex-wrap gap-x-3 gap-y-0.5 text-sm text-ink-soft tabular-nums">
                    {look("meta").map((c) => (
                      <span key={c.id} className="min-w-0">
                        <span className="text-ink-faint">{headerText(c.id)}</span> <span className="text-ink">{cell(row, c.id)}</span>
                      </span>
                    ))}
                  </div>
                </div>
                <div className="flex flex-none items-start gap-2">
                  {look("end").map((c) => (
                    <Fragment key={c.id}>{cell(row, c.id)}</Fragment>
                  ))}
                  {detail && <DetailToggle open={!!open[row.id]} onClick={() => toggle(row.id)} />}
                </div>
              </div>
              {look("foot").map((c) => (
                <div key={c.id} className="mt-2">
                  {cell(row, c.id)}
                </div>
              ))}
              {detail && open[row.id] && <div className="mt-2 rounded-tab bg-paper-lo/50 p-3">{detail(row.original)}</div>}
            </li>
          );
        })}
      </ul>

      {paged && (
      <Pager
        page={table.getState().pagination.pageIndex}
        pages={table.getPageCount()}
        total={data.length}
        size={pageSize}
        onPage={(p) => table.setPageIndex(p)}
      />
      )}
    </div>
  );
}

function DetailToggle({ open, onClick }: { open: boolean; onClick: () => void }) {
  return (
    <button
      type="button"
      aria-expanded={open}
      aria-label={open ? "Hide details" : "Show details"}
      onClick={onClick}
      className="inline-flex size-11 cursor-pointer items-center justify-center rounded-field text-ink-soft hover:text-ink"
    >
      <span aria-hidden="true">{open ? "▾" : "▸"}</span>
    </button>
  );
}

function Pager({ page, pages, total, size, onPage }: { page: number; pages: number; total: number; size: number; onPage: (p: number) => void }) {
  if (pages <= 1) return <p className="my-2 text-sm text-ink-soft">{total} shown</p>;
  const a = page * size + 1;
  const b = Math.min(total, (page + 1) * size);
  return (
    <div className="my-2 flex flex-wrap items-center gap-2 text-sm text-ink-soft">
      <Button disabled={page === 0} onClick={() => onPage(page - 1)}>
        Previous
      </Button>
      <span>
        {a}–{b} of {total}
      </span>
      <Button disabled={page >= pages - 1} onClick={() => onPage(page + 1)}>
        Next
      </Button>
    </div>
  );
}
