// Admin > API: API calls, new accounts and new sites per day against the
// period before, and a world map of where API calls come from. Reads the
// growth part of GET /v1/admin/overview?range=. Everything is drawn here as
// inline SVG; the map's shapes are /app/world-map.svg (Natural Earth, public
// domain). Nothing is fetched from anywhere else, so no visitor data leaves.
import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { Chips } from "@/components/ui/badge";
import { EmptyState } from "@/components/ui/empty-state";
import { Section } from "@/components/ui/page-shell";
import { dayLabel, num, short } from "@/lib/format";
import { cn } from "@/lib/utils";
import { useGrowth, type Growth, type Series } from "./data";

const RANGES = [
  ["7d", "7 days"],
  ["14d", "14 days"],
  ["30d", "30 days"],
  ["6m", "6 months"],
] as const;
type Range = (typeof RANGES)[number][0];
const WORD: Record<string, string> = Object.fromEntries(RANGES);
const GROUP: Record<string, string> = { calls: "API calls", deploy: "Deploy & sites", data: "Saved data", auth: "Sign-in & keys", connector: "Connector (MCP)", admin: "Admin", other: "Other" };
const CHART = ["var(--color-chart-1)", "var(--color-chart-2)", "var(--color-chart-3)", "var(--color-chart-4)", "var(--color-chart-5)", "var(--color-chart-6)"];
const SEQ = [0, 1, 2, 3, 4, 5].map((i) => `var(--color-seq-${i})`);

function savedRange(): Range {
  try {
    const r = localStorage.getItem("adminGrowthRange");
    if (r && r in WORD) return r as Range;
  } catch {
    /* storage may be off */
  }
  return "30d";
}

type Tip = { x: number; y: number; body: ReactNode } | null;

export function GrowthTab() {
  const [range, setRange] = useState<Range>(savedRange);
  const q = useGrowth(range);
  const [tip, setTip] = useState<Tip>(null);
  const choose = (r: Range) => {
    setRange(r);
    try {
      localStorage.setItem("adminGrowthRange", r);
    } catch {
      /* storage may be off */
    }
  };
  const g = q.data;
  return (
    <div onPointerLeave={() => setTip(null)}>
      <Section title="Growth">
        <Chips label="Time range" options={RANGES} value={range} onChange={choose} />
        {q.isError && !g && <EmptyState title="Could not load growth">{(q.error as Error).message}</EmptyState>}
        {g && (
          <>
            <p className="mt-1 mb-2.5 text-sm text-ink-soft">
              {dayLabel(g.from, true)} to {dayLabel(g.to, true)}, UTC days.
              {g.self_calls ? " API calls leave out " + num(g.self_calls) + " from this server (its own checks)." : ""}
            </p>
            <div className="mt-3 grid grid-cols-1 gap-4 md:grid-cols-2" aria-busy={q.isFetching}>
              <SeriesCard g={g} title="API calls" noun="calls" s={g.api} groups={g.group_order.map((k) => ({ key: k, vals: g.api.groups[k] || [] }))} wide setTip={setTip} />
              <SeriesCard g={g} title="New accounts" noun="new accounts" s={g.users} groups={[{ key: "users", vals: g.users.daily }]} setTip={setTip} />
              <SeriesCard g={g} title="New sites" noun="new sites" s={g.sites} groups={[{ key: "sites", vals: g.sites.daily }]} setTip={setTip} />
            </div>
          </>
        )}
      </Section>
      {g && (
        <Section title="Where API calls come from">
          <WorldMap g={g} setTip={setTip} />
          <p className="mt-2 text-xs text-ink-soft">
            Countries are looked up on this server from a local copy of <a href="https://db-ip.com">DB-IP</a>; no address is stored with these counts or sent anywhere.
          </p>
        </Section>
      )}
      {tip && (
        <div
          role="status"
          className="pointer-events-none fixed z-50 max-w-[min(280px,calc(100vw-16px))] rounded-field border border-ink-soft bg-card px-2.5 py-2 text-xs tabular-nums shadow-pop"
          style={{ left: tip.x, top: tip.y }}
        >
          {tip.body}
        </div>
      )}
    </div>
  );
}

function changeText(g: Growth, s: Series, noun: string): ReactNode {
  const w = "previous " + WORD[g.range];
  if (s.change_pct == null) {
    if (noun === "calls" && g.tracking_since && g.tracking_since > g.from) return "Counted since " + dayLabel(g.tracking_since, true);
    return s.previous > 0 ? "" : s.total > 0 ? "None in the " + w : "";
  }
  const p = s.change_pct;
  return (
    <>
      <b className="font-semibold text-ink">
        {p > 0 ? "+" : p < 0 ? "−" : "±"}
        {num(Math.abs(p))}%
      </b>{" "}
      vs {w} ({num(s.previous)})
    </>
  );
}

function useWidth<T extends HTMLElement>() {
  const ref = useRef<T>(null);
  const [w, setW] = useState(0);
  useLayoutEffect(() => {
    if (!ref.current) return;
    const ro = new ResizeObserver(([e]) => setW(Math.round(e.contentRect.width)));
    ro.observe(ref.current);
    return () => ro.disconnect();
  }, []);
  return [ref, w] as const;
}

/** placeTip keeps the tooltip on screen, above and right of the pointer. */
function at(e: { clientX: number; clientY: number }, body: ReactNode): Tip {
  const vw = document.documentElement.clientWidth;
  return { x: Math.max(8, Math.min(e.clientX + 14, vw - 288)), y: Math.max(8, e.clientY - 72), body };
}

function SeriesCard({
  g,
  title,
  noun,
  s,
  groups,
  wide,
  setTip,
}: {
  g: Growth;
  title: string;
  noun: string;
  s: Series;
  groups: { key: string; vals: number[] }[];
  wide?: boolean;
  setTip: (t: Tip) => void;
}) {
  const [ref, width] = useWidth<HTMLDivElement>();
  const [hover, setHover] = useState(-1);
  // Six months is shown per week (26 bars): a day would be under 2 px wide.
  const size = g.range === "6m" ? 7 : 1;
  const buckets: { from: number; to: number }[] = [];
  for (let i = 0; i < g.dates.length; i += size) buckets.push({ from: i, to: Math.min(g.dates.length, i + size) - 1 });
  const sum = (vals: number[], b: { from: number; to: number }) => {
    let t = 0;
    for (let i = b.from; i <= b.to; i++) t += vals[i] || 0;
    return t;
  };
  const totals = buckets.map((b) => groups.reduce((a, x) => a + sum(x.vals, b), 0));
  const top = Math.max(0, ...totals);
  const max = niceMax(top);
  const W = Math.max(240, width || 600);
  const H = wide ? 190 : 140;
  const padL = 34, padR = 4, padT = 8, padB = 22;
  const iw = W - padL - padR, ih = H - padT - padB;
  const n = buckets.length, slot = iw / n, gap = Math.min(2, slot * 0.25), bw = Math.max(1, slot - gap);
  const color = (i: number) => (groups.length > 1 ? CHART[i % CHART.length] : "var(--color-chart-1)");
  const when = (i: number) => (size > 1 ? "Week of " + dayLabel(g.dates[buckets[i].from], true) : dayLabel(g.dates[buckets[i].from], true));
  const labels = [...new Set([0, Math.floor((n - 1) / 2), n - 1])];

  const point = (e: React.PointerEvent<SVGSVGElement>) => {
    const r = e.currentTarget.getBoundingClientRect();
    const i = Math.floor(((e.clientX - r.left) * (W / r.width) - padL) / slot);
    if (i < 0 || i >= n) {
      setHover(-1);
      setTip(null);
      return;
    }
    setHover(i);
    setTip(
      at(
        e,
        <>
          <div className="text-ink-soft">{when(i)}</div>
          <strong>
            {num(totals[i])} {noun}
          </strong>
          {groups.length > 1 &&
            groups.map((x, k) => {
              const v = sum(x.vals, buckets[i]);
              return v ? (
                <div key={x.key} className="flex justify-between gap-3.5">
                  <span>
                    <i className="mr-1.5 inline-block size-2 rounded-[2px]" style={{ background: color(k) }} />
                    {GROUP[x.key] || x.key}
                  </span>
                  <span>{num(v)}</span>
                </div>
              ) : null;
            })}
        </>,
      ),
    );
  };

  return (
    <div className={cn("min-w-0 rounded-tab border border-rule bg-card px-3.5 pt-3.5 pb-2.5", wide && "md:col-span-2")}>
      <h3 className="text-sm font-semibold text-ink-soft">{title}</h3>
      <div className="mt-0.5 text-2xl font-semibold tabular-nums">{num(s.total)}</div>
      <div className="mt-0.5 min-h-[18px] text-xs text-ink-soft">{changeText(g, s, noun === "calls" ? "calls" : noun)}</div>
      <div ref={ref} className="mt-2.5 w-full min-w-0 touch-pan-y">
        <svg
          viewBox={`0 0 ${W} ${H}`}
          role="img"
          aria-label={`${num(s.total)} ${noun} from ${dayLabel(g.from, true)} to ${dayLabel(g.to, true)}; highest ${size > 1 ? "week" : "day"} ${num(top)}`}
          className="block h-auto w-full overflow-visible"
          onPointerMove={point}
          onPointerDown={point}
          onPointerLeave={(e) => {
            if (e.pointerType === "mouse") {
              setHover(-1);
              setTip(null);
            }
          }}
        >
          {[0, 0.5, 1].map((f) => {
            const y = padT + ih - ih * f;
            return (
              <g key={f}>
                <line x1={padL} x2={W - padR} y1={y} y2={y} stroke={f === 0 ? "var(--color-ink-soft)" : "var(--color-rule)"} strokeWidth={1} />
                <text x={padL - 6} y={y + 3.5} textAnchor="end" fontSize={11} fill="var(--color-ink-soft)">
                  {short(max * f)}
                </text>
              </g>
            );
          })}
          {hover >= 0 && <rect x={padL + hover * slot} y={padT} width={slot} height={ih} fill="var(--color-paper-lo)" />}
          {buckets.map((b, i) => {
            let y0 = padT + ih;
            const x = padL + i * slot + gap / 2;
            return groups.map((grp, k) => {
              const v = sum(grp.vals, b);
              if (!v) return null;
              const h = (v / max) * ih;
              y0 -= h;
              return <rect key={i + ":" + k} x={x} y={y0} width={bw} height={Math.max(0.5, h - (k > 0 && h > 2 ? 1 : 0))} fill={color(k)} rx={bw >= 4 ? 2 : 0} />;
            });
          })}
          {labels.map((i, k) => {
            const anchor = k === 0 ? "start" : i === n - 1 ? "end" : "middle";
            const x = anchor === "start" ? padL : anchor === "end" ? W - padR : padL + i * slot + slot / 2;
            return (
              <text key={i} x={x} y={H - 6} textAnchor={anchor} fontSize={11} fill="var(--color-ink-soft)">
                {dayLabel(g.dates[buckets[i].from])}
              </text>
            );
          })}
        </svg>
      </div>
      <div className="mt-1 text-xs text-ink-soft">{size > 1 ? "Per week (weeks start " + dayLabel(g.dates[0]) + ")." : "Per day."}</div>
      {wide && groups.length > 1 && (
        <div className="mt-2 flex flex-wrap gap-x-3.5 gap-y-1 text-xs text-ink-soft">
          {groups.map((x, k) => (
            <span key={x.key} className="inline-flex items-center gap-1.5 whitespace-nowrap">
              <i className="inline-block size-2.5 rounded-[3px]" style={{ background: color(k) }} />
              {GROUP[x.key] || x.key} <b className="font-semibold text-ink">{num(x.vals.reduce((a, v) => a + v, 0))}</b>
            </span>
          ))}
        </div>
      )}
    </div>
  );
}

function niceMax(v: number) {
  if (v <= 4) return 4;
  const p = Math.pow(10, Math.floor(Math.log10(v)));
  const f = v / p;
  return (f <= 1 ? 1 : f <= 2 ? 2 : f <= 2.5 ? 2.5 : f <= 5 ? 5 : 10) * p;
}

function bin(v: number, max: number) {
  if (!v) return 0;
  if (max <= 1) return 5;
  return Math.max(1, Math.min(5, Math.ceil((Math.log(1 + v) / Math.log(1 + max)) * 5)));
}

function WorldMap({ g, setTip }: { g: Growth; setTip: (t: Tip) => void }) {
  const map = useQuery({
    queryKey: ["world-map"],
    queryFn: async () => {
      const r = await fetch("/app/world-map.svg");
      if (!r.ok) throw new Error("The map could not be loaded.");
      return r.text();
    },
    staleTime: Infinity,
  });
  const box = useRef<HTMLDivElement>(null);
  const cs = g.countries || [];
  const max = cs.length ? cs[0].calls : 0;
  const total = cs.reduce((a, c) => a + c.calls, 0);
  const byCC = Object.fromEntries(cs.map((c) => [c.code, c]));

  useEffect(() => {
    const svg = box.current?.querySelector("svg");
    if (!svg) return;
    svg.setAttribute("aria-hidden", "true");
    svg.querySelectorAll<SVGPathElement>("path[data-cc]").forEach((p) => {
      const c = byCC[p.getAttribute("data-cc") || ""];
      p.style.fill = SEQ[bin(c ? c.calls : 0, max)];
      p.style.stroke = "var(--color-card)";
      p.style.strokeWidth = "0.5";
    });
  });

  const point = (e: React.PointerEvent<HTMLDivElement>) => {
    const p = (e.target as Element).closest?.("path[data-cc]");
    if (!p) return setTip(null);
    const c = byCC[p.getAttribute("data-cc") || ""];
    setTip(
      at(
        e,
        <>
          <div className="text-ink-soft">{c?.name || p.getAttribute("data-name")}</div>
          <strong>{c ? num(c.calls) + " call" + (c.calls === 1 ? "" : "s") : "No calls"}</strong>
        </>,
      ),
    );
  };

  return (
    <>
      <p className="mt-1 mb-2.5 text-sm text-ink-soft">
        {cs.length} {cs.length === 1 ? "country" : "countries"}, {num(total)} calls.
        {g.unknown_country_calls ? " " + num(g.unknown_country_calls) + " more came from an address with no known country." : ""}
      </p>
      <div className="mt-3 grid grid-cols-1 gap-4 lg:grid-cols-[minmax(0,2fr)_minmax(240px,1fr)]">
        <div className="min-w-0">
          {map.isError ? (
            <p className="text-sm text-ink-soft">The map could not be loaded.</p>
          ) : (
            <div
              ref={box}
              role="img"
              aria-label={"World map of API calls by country. Top: " + cs.slice(0, 3).map((c) => c.name + " " + num(c.calls)).join(", ")}
              className="min-h-24 [&_svg]:block [&_svg]:h-auto [&_svg]:w-full"
              onPointerMove={point}
              onPointerDown={point}
              dangerouslySetInnerHTML={{ __html: map.data || "" }}
            />
          )}
          {max > 0 && (
            <div className="mt-2 flex flex-wrap items-center gap-1.5 text-xs text-ink-soft tabular-nums">
              <span>1</span>
              <span className="flex">
                {[1, 2, 3, 4, 5].map((k) => (
                  <i key={k} className="block h-2.5 w-5.5 first:rounded-l-[3px] last:rounded-r-[3px]" style={{ background: SEQ[k] }} />
                ))}
              </span>
              <span>{num(max)} calls</span>
              <i className="ml-2.5 block h-2.5 w-5.5 rounded-[3px]" style={{ background: SEQ[0] }} />
              <span>none</span>
            </div>
          )}
        </div>
        <ol className="m-0 list-none p-0 text-sm">
          {cs.length === 0 && <li className="py-2 text-ink-soft">No calls with a known country in this range.</li>}
          {cs.slice(0, 10).map((c, i) => (
            <li key={c.code} className="grid grid-cols-[22px_minmax(0,1fr)_auto] items-baseline gap-x-2 gap-y-1 border-b border-rule py-1.5">
              <span className="text-xs text-ink-soft tabular-nums">{i + 1}</span>
              <span>{c.name || c.code}</span>
              <span className="text-right tabular-nums">
                {num(c.calls)}
                <small className="ml-1.5 text-xs text-ink-soft">{total ? Math.round((c.calls / total) * 1000) / 10 : 0}%</small>
              </span>
              <span className="col-[2/-1] h-1 overflow-hidden rounded-tag bg-rule">
                <span className="block h-full rounded-tag bg-chart-1" style={{ width: (max ? Math.max(2, (c.calls / max) * 100) : 0) + "%" }} />
              </span>
            </li>
          ))}
        </ol>
      </div>
    </>
  );
}
