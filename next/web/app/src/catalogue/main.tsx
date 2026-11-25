// The screen catalogue (catalogue.html, never in the production build):
//   catalogue.html            the list of entries, each at 390 and 1280 side by side
//   catalogue.html?id=<id>    one entry on its own page (what the visual checks open);
//                             <body data-ready> is set once its steps have run.
import { StrictMode, useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import { MemoryRouter, Route, Routes } from "react-router";
import { QueryClient } from "@tanstack/react-query";
import "../styles/app.css";
import { Providers } from "../App";
import { ApiError, type Call } from "@/lib/api";
import { SignIn } from "@/screens/signin/SignIn";
import { Admin } from "@/screens/admin/Admin";
import { entries, type Entry, type Step } from "./entries";

let pending = 0;
let started = false; // StrictMode runs effects twice in development

function fakeCall(e: Entry): Call {
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

const visible = (el: Element) => (el as HTMLElement).getClientRects().length > 0;

function find(sel: string): HTMLElement | undefined {
  const [what, nth] = sel.split("@");
  let all: Element[];
  if (what.startsWith("text=")) {
    const text = what.slice(5);
    all = [...document.querySelectorAll("button, a, [role=tab], [role=menuitem], label")].filter((el) => el.textContent?.trim() === text);
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
      const el = (await waitFor(sel)) as HTMLInputElement | HTMLSelectElement;
      const proto = el instanceof HTMLSelectElement ? HTMLSelectElement.prototype : HTMLInputElement.prototype;
      Object.getOwnPropertyDescriptor(proto, "value")!.set!.call(el, value);
      el.dispatchEvent(new Event("fill" in s ? "input" : "change", { bubbles: true }));
    }
    await settle();
  }
  await document.fonts.ready;
  document.body.dataset.ready = "1";
}

function One({ e }: { e: Entry }) {
  const [client] = useState(() => new QueryClient({ defaultOptions: { queries: { retry: false, refetchOnWindowFocus: false } } }));
  useEffect(() => {
    if (started) return;
    started = true;
    run(e.steps || []).catch((err) => {
      console.error(String(err));
      document.body.dataset.ready = "1";
    });
  }, [e]);
  return (
    <Providers call={fakeCall(e)} client={client}>
      <MemoryRouter initialEntries={[e.url]}>
        <Routes>
          <Route path="/signin" element={<SignIn />} />
          <Route path="/admin" element={<Admin />} />
        </Routes>
      </MemoryRouter>
    </Providers>
  );
}

function Index() {
  return (
    <main className="mx-auto max-w-page p-6">
      <h1 className="type-page">Screen catalogue</h1>
      <p className="mb-6 text-ink-soft">
        {entries.length} entries, each shown at 390 and 1280 px. Open one to see it full size.
      </p>
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

const id = new URLSearchParams(location.search).get("id");
const entry = entries.find((e) => e.id === id);
if (entry) document.querySelector('meta[name="sh-google"]')!.setAttribute("content", entry.google ? "1" : "");

createRoot(document.getElementById("root")!).render(<StrictMode>{entry ? <One e={entry} /> : <Index />}</StrictMode>);
