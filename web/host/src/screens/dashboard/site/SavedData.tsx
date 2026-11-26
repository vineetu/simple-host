// Saved data: what the site's pages have stored, by store (namespace): its
// tables, values and files to look through, SQL for anything else, and who
// may read and write each part.
//   POST /v1/sites/{site}/storage/sqlite/{ns}/query|execute  {sql}
//   GET|DELETE /v1/sites/{site}/storage/kv/{ns}/keys[/{key}]
//   GET|DELETE /v1/sites/{site}/storage/files/{ns}/objects[/{path}]
//   PUT /v1/sites/{site}/storage/{ns}/rules  {rules, spam_check}
import { useState, type ReactNode } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useApi } from "@/lib/api";
import { bytes, num } from "@kit/lib/format";
import { Chips } from "@kit/components/ui/badge";
import { Button } from "@kit/components/ui/button";
import { Check } from "@kit/components/ui/choice";
import { AskDialog } from "@kit/components/ui/dialog";
import { FieldMessage, Input, Label, NativeSelect, Textarea } from "@kit/components/ui/input";
import { Meter } from "@kit/components/ui/meter";
import { Skeleton } from "@kit/components/ui/skeleton";
import { useToast } from "@kit/components/ui/toast";
import { sitePath, useMe, type Namespace, type Site } from "../data";
import { Part, Row } from "../bits";

type Result = { columns: string[]; rows: unknown[][] };

export function SavedData({ site }: { site: Site }) {
  const [pick, setPick] = useState(site.namespaces[0]?.name || "");
  const ns = site.namespaces.find((n) => n.name === pick) || site.namespaces[0];
  const u = site.use;
  return (
    <>
      <Part title="Saved data" note="What the site's pages have saved: bookings, messages, sign-ups, photos. You can look through it here, or ask your AI app, for example “show my bookings”.">
        <Meter
          label="Saved data used"
          value={u.data_bytes}
          max={u.data_bytes_limit}
          text={bytes(u.data_bytes) + " of " + bytes(u.data_bytes_limit) + " used"}
        />
      </Part>
      {!ns ? (
        <p className="my-6 max-w-[70ch] text-ink-soft">
          Nothing saved yet. A store appears here when you ask your AI app for a page that saves things, such as a booking form or a guest book.
        </p>
      ) : (
        <>
          {site.namespaces.length > 1 && (
            <Chips label="Store" options={site.namespaces.map((n) => [n.name, n.name] as const)} value={ns.name} onChange={setPick} />
          )}
          <Store key={ns.name} site={site} ns={ns} />
        </>
      )}
      <Sql site={site} start={ns?.name || ""} />
    </>
  );
}

function Store({ site, ns }: { site: Site; ns: Namespace }) {
  const tables = ns.tables.filter((t) => !t.startsWith("_"));
  return (
    <>
      <h2 className="type-section mt-6">
        {ns.name} <span className="text-sm font-normal text-ink-soft">· {bytes(ns.bytes)}</span>
      </h2>
      {tables.length > 0 && (
        <Part title="Tables" className="mt-4 first:mt-4">
          <ul className="m-0 list-none p-0">
            {tables.map((t) => (
              <Table key={t} site={site} ns={ns.name} table={t} />
            ))}
          </ul>
        </Part>
      )}
      {ns.tables.includes("_values") && <Values site={site} ns={ns.name} />}
      {ns.tables.includes("_files") && <FileList site={site} ns={ns.name} />}
      <Rules site={site} ns={ns} />
    </>
  );
}

/** Table is one table's name; Show rows opens its newest 50. */
function Table({ site, ns, table }: { site: Site; ns: string; table: string }) {
  const me = useMe().data;
  const call = useApi();
  const [open, setOpen] = useState(false);
  const q = useQuery({
    queryKey: ["rows", site.owner, site.name, ns, table],
    queryFn: () =>
      call<Result>("POST", sitePath(site, me, "/storage/sqlite/" + encodeURIComponent(ns) + "/query"), {
        sql: `SELECT * FROM "${table}" ORDER BY rowid DESC LIMIT 50`,
      }),
    enabled: open,
  });
  return (
    <li className="border-b border-rule py-2 first:border-t">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <span className="font-semibold">{table}</span>
        <Button variant="ghost" className="px-3" aria-expanded={open} onClick={() => setOpen(!open)}>
          {open ? "Hide rows" : "Show rows"}
        </Button>
      </div>
      {open && (q.isPending ? <Skeleton className="my-2 h-24" /> : q.isError ? <FieldMessage tone="bad">{(q.error as Error).message}</FieldMessage> : <ResultTable r={q.data} label={table} note="The newest 50 rows." />)}
    </li>
  );
}

/** ResultTable is rows from a query; it scrolls sideways inside itself when wide. */
function ResultTable({ r, label, note }: { r: Result; label: string; note?: string }) {
  if (!r.rows.length) return <p className="my-2 text-sm text-ink-soft">No rows.</p>;
  return (
    <>
      <div data-scroll-x tabIndex={0} role="region" aria-label={label + " rows"} className="my-2 overflow-x-auto rounded-field border border-rule bg-card">
        <table className="w-max min-w-full border-collapse text-sm">
          <thead>
            <tr>
              {r.columns.map((c) => (
                <th key={c} className="border-b border-rule bg-paper-lo px-2.5 py-1.5 text-left type-label whitespace-nowrap">
                  {c}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {r.rows.map((row, i) => (
              <tr key={i}>
                {row.map((v, j) => (
                  <td key={j} className="max-w-72 min-w-[6ch] border-b border-rule px-2.5 py-1.5 align-top [overflow-wrap:break-word]">
                    {v == null ? <span className="text-ink-faint">empty</span> : typeof v === "object" ? JSON.stringify(v) : String(v)}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <p className="text-xs text-ink-soft">
        {num(r.rows.length)} {r.rows.length === 1 ? "row" : "rows"}. {note}
      </p>
    </>
  );
}

/** Items is a list read from the values or files routes, with Delete (asked first: it cannot be undone). */
function Items<T>({
  site,
  title,
  note,
  path,
  name,
  line,
  open,
  none,
}: {
  site: Site;
  title: string;
  note: string;
  path: string;
  name: (it: T) => string;
  line: (it: T) => ReactNode;
  open?: (it: T) => string;
  none: string;
}) {
  const me = useMe().data;
  const call = useApi();
  const qc = useQueryClient();
  const toast = useToast();
  const [gone, setGone] = useState<string | null>(null);
  const key = ["items", site.owner, site.name, path];
  const q = useQuery({ queryKey: key, queryFn: () => call<{ items: T[]; next_after: string }>("GET", sitePath(site, me, path, { limit: "100" })) });
  return (
    <Part title={title} note={note} className="mt-6 first:mt-6">
      {q.isPending ? (
        <Skeleton className="h-20" />
      ) : q.isError ? (
        <FieldMessage tone="bad">{(q.error as Error).message}</FieldMessage>
      ) : q.data.items.length ? (
        <ul className="m-0 list-none p-0">
          {q.data.items.map((it) => (
            <Row
              key={name(it)}
              end={
                <>
                  {open && (
                    <Button asChild variant="ghost" className="px-3">
                      <a href={open(it)} target="_blank" rel="noopener" aria-label={"Open " + name(it)}>
                        Open
                      </a>
                    </Button>
                  )}
                  <Button variant="ghost" className="px-3 text-bad" onClick={() => setGone(name(it))} aria-label={"Delete " + name(it)}>
                    Delete
                  </Button>
                </>
              }
            >
              <p className="font-semibold break-all">{name(it)}</p>
              <div className="text-sm break-all text-ink-soft">{line(it)}</div>
            </Row>
          ))}
        </ul>
      ) : (
        <p className="text-sm text-ink-soft">{none}</p>
      )}
      {q.data?.next_after && <p className="mt-2 text-sm text-ink-soft">The first 100 are shown. Ask your AI app for the rest.</p>}
      <AskDialog
        open={gone !== null}
        onOpenChange={(o) => !o && setGone(null)}
        title={"Delete " + (gone || "") + "?"}
        body="It is removed from the site's saved data at once, and cannot be brought back."
        confirmLabel="Delete"
        danger
        onConfirm={async () => {
          try {
            await call("DELETE", sitePath(site, me, path + "/" + gone!.split("/").map(encodeURIComponent).join("/")));
            await qc.invalidateQueries({ queryKey: key });
            await qc.invalidateQueries({ queryKey: ["site", site.owner, site.name] });
            toast("Deleted " + gone + ".");
          } catch (e) {
            toast((e as Error).message, { bad: true });
          }
        }}
      />
    </Part>
  );
}

type Value = { key: string; value: unknown };
function Values({ site, ns }: { site: Site; ns: string }) {
  return (
    <Items<Value>
      site={site}
      title="Values"
      note="Single entries saved by name, like settings or a counter."
      path={"/storage/kv/" + encodeURIComponent(ns) + "/keys"}
      name={(v) => v.key}
      line={(v) => {
        const t = JSON.stringify(v.value);
        return <code className="font-mono text-xs">{t.length > 160 ? t.slice(0, 160) + "…" : t}</code>;
      }}
      none="No values saved yet."
    />
  );
}

type Stored = { path: string; bytes: number; content_type: string };
function FileList({ site, ns }: { site: Site; ns: string }) {
  const me = useMe().data;
  const path = "/storage/files/" + encodeURIComponent(ns) + "/objects";
  return (
    <Items<Stored>
      site={site}
      title="Files"
      note="Photos and files the site keeps, like uploads from visitors."
      path={path}
      name={(f) => f.path}
      line={(f) => bytes(f.bytes) + (f.content_type ? " · " + f.content_type : "")}
      open={(f) => sitePath(site, me, path + "/" + f.path.split("/").map(encodeURIComponent).join("/"))}
      none="No files saved yet."
    />
  );
}

/** Rules is who may read, add, change and delete each part of a store. */
function Rules({ site, ns }: { site: Site; ns: Namespace }) {
  const me = useMe().data;
  const call = useApi();
  const qc = useQueryClient();
  const toast = useToast();
  const [text, setText] = useState(ns.rules ? JSON.stringify(ns.rules, null, 2) : "{}");
  const [spam, setSpam] = useState(ns.spam_check);
  const [error, setError] = useState("");
  return (
    <details className="group mt-8 border-t border-rule pt-2">
      <summary className="flex min-h-11 cursor-pointer list-none items-center gap-2 type-section [&::-webkit-details-marker]:hidden">
        <Caret />
        Who may read and write {ns.name}
      </summary>
      <p className="mt-1 mb-3 max-w-[70ch] text-sm text-ink-soft">
        For each table, and for “_values” and “_files”: who may read, add, change and delete (anyone, signed-in people, the person who added it,
        or only you). With no rules, only you can.
      </p>
      <Label htmlFor={"rules-" + ns.name}>Rules</Label>
      <Textarea id={"rules-" + ns.name} code rows={8} value={text} onChange={(e) => (setText(e.target.value), setError(""))} spellCheck={false} aria-invalid={!!error} />
      <Check checked={spam} onChange={setSpam}>
        Visitors who are not signed in pass a spam check before they add anything
      </Check>
      <Button
        onClick={async () => {
          let rules: unknown;
          try {
            rules = JSON.parse(text || "{}");
          } catch (e) {
            return setError("The rules are not valid JSON: " + (e as Error).message);
          }
          try {
            await call("PUT", sitePath(site, me, "/storage/" + encodeURIComponent(ns.name) + "/rules"), { rules, spam_check: spam });
            await qc.invalidateQueries({ queryKey: ["site", site.owner, site.name] });
            toast("Rules saved for " + ns.name + ".");
          } catch (e) {
            setError((e as Error).message);
          }
        }}
      >
        Save rules
      </Button>
      {error && <FieldMessage tone="bad">{error}</FieldMessage>}
    </details>
  );
}

/** Sql runs one statement in a store; a new store name makes the store. */
function Sql({ site, start }: { site: Site; start: string }) {
  const me = useMe().data;
  const call = useApi();
  const qc = useQueryClient();
  const [ns, setNs] = useState(start || "");
  const [fresh, setFresh] = useState("");
  const [sql, setSql] = useState("");
  const [out, setOut] = useState<Result | string | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const target = ns === "" ? fresh.trim().toLowerCase() : ns;
  return (
    <details className="group mt-8 border-t border-rule pt-2">
      <summary className="flex min-h-11 cursor-pointer list-none items-center gap-2 type-section [&::-webkit-details-marker]:hidden">
        <Caret />
        Run SQL
      </summary>
      <p className="mt-1 mb-3 max-w-[70ch] text-sm text-ink-soft">
        For anything the lists above do not do. A SELECT shows rows; anything else changes data and says how many rows it changed.
      </p>
      <form
        noValidate
        onSubmit={async (e) => {
          e.preventDefault();
          if (!target) return setError("Choose a store, or name a new one.");
          if (!sql.trim()) return setError("Write a statement to run.");
          const reads = /^\s*(select|with|pragma|explain)\b/i.test(sql);
          setBusy(true);
          setError("");
          try {
            const r = await call<Result & { changes?: number }>(
              "POST",
              sitePath(site, me, "/storage/sqlite/" + encodeURIComponent(target) + (reads ? "/query" : "/execute")),
              { sql },
            );
            setOut(reads ? r : (r.changes === 1 ? "1 row changed." : num(r.changes || 0) + " rows changed."));
            if (!reads) await qc.invalidateQueries({ queryKey: ["site", site.owner, site.name] });
          } catch (err) {
            setError((err as Error).message);
          } finally {
            setBusy(false);
          }
        }}
      >
        <div className="mb-3 flex flex-wrap gap-2">
          <div className="flex-[1_1_12rem]">
            <Label htmlFor="sql-store">Store</Label>
            <NativeSelect id="sql-store" className="w-full" value={ns} onChange={(e) => setNs(e.target.value)}>
              {site.namespaces.map((n) => (
                <option key={n.name} value={n.name}>
                  {n.name}
                </option>
              ))}
              <option value="">A new store…</option>
            </NativeSelect>
          </div>
          {ns === "" && (
            <div className="flex-[1_1_12rem]">
              <Label htmlFor="sql-new">New store's name</Label>
              <Input id="sql-new" value={fresh} onChange={(e) => setFresh(e.target.value)} placeholder="bookings" autoComplete="off" spellCheck={false} />
            </div>
          )}
        </div>
        <Label htmlFor="sql">Statement</Label>
        <Textarea id="sql" code rows={4} value={sql} onChange={(e) => setSql(e.target.value)} placeholder="SELECT * FROM bookings ORDER BY id DESC LIMIT 20" spellCheck={false} />
        <div className="mt-3">
          <Button type="submit" disabled={busy}>
            {busy ? "Running…" : "Run"}
          </Button>
        </div>
        {error && <FieldMessage tone="bad">{error}</FieldMessage>}
      </form>
      {typeof out === "string" ? <p className="mt-3" role="status">{out}</p> : out && <ResultTable r={out} label="Result" />}
    </details>
  );
}

const Caret = () => (
  <span aria-hidden="true" className="inline-block text-ink-soft transition-transform group-open:rotate-90">
    ▸
  </span>
);
