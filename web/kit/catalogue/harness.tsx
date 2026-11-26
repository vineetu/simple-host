// The screen catalogue's machinery, shared by the apps (catalogue.html, never
// in a production build):
//   catalogue.html            the list of entries, each at 390 and 1280 side by side
//   catalogue.html?id=<id>    one entry on its own page (what the visual checks open);
//                             <body data-ready> is set once its steps have run.
// An entry is a URL inside the app, the API's answers for it (fixtures) and
// optional steps (click, fill, select) to reach a dialog or a later step:
// fill types into an input or a textarea, so a state that needs words typed
// (a message the server refuses) is one fill and one click.
// (Simple Host's owner app keeps its own copy in web/host/src/catalogue/main.tsx for now.)
import { StrictMode, useEffect, useState, type ReactNode } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient } from "@tanstack/react-query";
import { ApiError, type Call } from "../api/call";

/** An answer: a value, a refusal, or "pending" (the request never ends). */
export type Answer = unknown | ApiError | "pending";
export type Route = (method: string, path: string, body: unknown) => Answer;
export type Step = { click: string } | { fill: [string, string] } | { select: [string, string] };

export type Entry = {
  id: string;
  title: string;
  url: string;
  api: Route;
  steps?: Step[];
  /** Runs before the screen mounts (browser storage the screen reads). */
  before?: () => void;
};

export const refuse = (status: number, code: string, message: string, body: Record<string, unknown> = {}) =>
  new ApiError(status, code, message, { error: message, code, ...body });

let pending = 0;
let started = false; // StrictMode runs effects twice in development

/** fakeCall answers every request from the entry's api, a little later, as the service would. */
export function fakeCall(e: Entry): Call {
  return <T,>(method: string, path: string, body?: unknown) => {
    const a = e.api(method, path, body);
    if (a === "pending") return new Promise<T>(() => {});
    pending++;
    return new Promise<T>((resolve, reject) =>
      setTimeout(() => {
        pending--;
        if (a instanceof ApiError) reject(a);
        else resolve(structuredClone(a) as T);
      }, 10),
    );
  };
}

/** track counts a request made outside the Call (bytes, an image) as in flight, so the steps wait for it. */
export async function track<T>(p: Promise<T>): Promise<T> {
  pending++;
  try {
    return await p;
  } finally {
    pending--;
  }
}

const visible = (el: Element) => (el as HTMLElement).getClientRects().length > 0;

function find(sel: string): HTMLElement | undefined {
  const [what, nth] = sel.split("@");
  let all: Element[];
  if (what.startsWith("text=")) {
    const text = what.slice(5);
    all = [...document.querySelectorAll("button, a, [role=tab], [role=menuitem], [role=radio], label")].filter((el) => el.textContent?.trim() === text);
  } else all = [...document.querySelectorAll(what)];
  return all.filter(visible)[Number(nth || 0)] as HTMLElement | undefined;
}

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

async function settle() {
  for (let i = 0; i < 100 && pending > 0; i++) await sleep(20);
  await sleep(60);
}

async function waitFor(sel: string) {
  for (let i = 0; i < 100; i++) {
    const el = find(sel);
    if (el) return el;
    await sleep(20);
  }
  throw new Error("catalogue step: nothing matches " + sel);
}

async function run(steps: Step[]) {
  await settle();
  for (const s of steps) {
    if ("click" in s) {
      const el = await waitFor(s.click);
      // Radix menus open on pointerdown; a plain click covers everything else.
      el.dispatchEvent(new PointerEvent("pointerdown", { bubbles: true, button: 0, pointerType: "mouse" }));
      el.click();
    } else {
      const [sel, value] = "fill" in s ? s.fill : s.select;
      // An input, a textarea or a select: through its own value setter, so React sees the change.
      const el = (await waitFor(sel)) as HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement;
      const proto = el instanceof HTMLSelectElement ? HTMLSelectElement.prototype : el instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
      Object.getOwnPropertyDescriptor(proto, "value")!.set!.call(el, value);
      el.dispatchEvent(new Event("fill" in s ? "input" : "change", { bubbles: true }));
    }
    await settle();
  }
  await document.fonts.ready;
  document.body.dataset.ready = "1";
}

function One({ e, render }: { e: Entry; render: (e: Entry, call: Call, client: QueryClient) => ReactNode }) {
  const [client] = useState(() => new QueryClient({ defaultOptions: { queries: { retry: false, refetchOnWindowFocus: false } } }));
  const [call] = useState(() => fakeCall(e));
  useEffect(() => {
    if (started) return;
    started = true;
    run(e.steps || []).catch((err) => {
      console.error(String(err));
      document.body.dataset.ready = "1";
    });
  }, [e]);
  return <>{render(e, call, client)}</>;
}

function Index({ entries }: { entries: Entry[] }) {
  return (
    <main className="mx-auto max-w-page p-6">
      <h1 className="type-page">Screen catalogue</h1>
      <p className="mb-6 text-ink-soft">{entries.length} entries, each shown at 390 and 1280 px. Open one to see it full size.</p>
      {entries.map((e) => (
        <section key={e.id} className="mb-10">
          <h2 className="mb-2 text-lg font-semibold">
            <a href={"?id=" + e.id}>{e.title}</a> <span className="text-sm font-normal text-ink-soft">{e.id}</span>
          </h2>
          <div className="flex items-start gap-4 overflow-x-auto">
            <iframe title={e.id + " at 390"} src={"?id=" + e.id} width={390} height={760} className="flex-none border border-rule bg-paper" />
            <div className="h-[760px] w-[640px] flex-none overflow-hidden border border-rule">
              <iframe title={e.id + " at 1280"} src={"?id=" + e.id} width={1280} height={1520} className="origin-top-left scale-50 bg-paper" />
            </div>
          </div>
        </section>
      ))}
    </main>
  );
}

/** mountCatalogue renders the list, or one entry through render (the app's providers and router around its screens). */
export function mountCatalogue(entries: Entry[], render: (e: Entry, call: Call, client: QueryClient) => ReactNode) {
  const ids = new Set<string>();
  for (const e of entries) {
    if (ids.has(e.id)) throw new Error("two catalogue entries are called " + e.id);
    ids.add(e.id);
  }
  const id = new URLSearchParams(location.search).get("id");
  const entry = entries.find((e) => e.id === id);
  entry?.before?.();
  createRoot(document.getElementById("root")!).render(<StrictMode>{entry ? <One e={entry} render={render} /> : <Index entries={entries} />}</StrictMode>);
}
