// The operator's actions: take a site down or restore it, suspend an account
// or re-enable it, make an account (self-hosted). Each reloads the lists.
//   PATCH /v1/sites/{site}?owner=<handle>  {taken_down, reason}
//   PATCH /v1/admin/people/{id}            {suspended, reason}
//   POST  /v1/admin/people                 {email}
import { createContext, useContext, useState, type ReactNode } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useApi } from "@/lib/api";
import { AskDialog } from "@kit/components/ui/dialog";
import { useToast } from "@kit/components/ui/toast";
import type { Person, Site } from "./data";

type Actions = {
  takeDown: (s: Site) => void;
  restore: (s: Site) => void;
  suspend: (u: Person) => void;
  enable: (u: Person) => void;
  makeAccount: (email: string) => Promise<boolean>;
};
const ActionsContext = createContext<Actions | null>(null);
export const useActions = () => useContext(ActionsContext)!;

type Asking = { kind: "takedown"; site: Site } | { kind: "suspend"; user: Person } | null;

export function ActionsProvider({ children }: { children: ReactNode }) {
  const call = useApi();
  const qc = useQueryClient();
  const toast = useToast();
  const [asking, setAsking] = useState<Asking>(null);

  // run does one change, reloads the lists and says so; undo (when given) is
  // offered in the note, so the safe direction never asks first.
  const run = async (method: string, path: string, body: unknown, done: string, undo?: () => void) => {
    try {
      const b = await call<{ says?: string }>(method, path, body);
      await qc.invalidateQueries({ queryKey: ["admin"] });
      toast(b.says || done, undo && { undo: { label: "Undo", run: undo } });
      return true;
    } catch (e) {
      toast((e as Error).message, { bad: true });
      return false;
    }
  };
  const sitePath = (s: Site) => "/v1/sites/" + encodeURIComponent(s.name) + "?owner=" + encodeURIComponent(s.owner);
  const who = (u: Person) => u.handle || u.email;

  const personPath = (u: Person) => "/v1/admin/people/" + encodeURIComponent(u.id);
  const takeDown = (s: Site, reason: string) =>
    run("PATCH", sitePath(s), { taken_down: true, reason }, "Took down " + s.name + ".", () => void restore(s, reason));
  const restore = (s: Site, reason: string) =>
    run("PATCH", sitePath(s), { taken_down: false }, "Restored " + s.name + ".", () => void takeDown(s, reason));
  const suspend = (u: Person, reason: string) =>
    run("PATCH", personPath(u), { suspended: true, reason }, "Suspended " + who(u) + ".", () => void enable(u, reason));
  const enable = (u: Person, reason: string) =>
    run("PATCH", personPath(u), { suspended: false }, "Re-enabled " + who(u) + ".", () => void suspend(u, reason));

  // Taking down and suspending ask first: they need a reason, and the owner
  // sees it. Restoring and re-enabling just happen, with Undo.
  const actions: Actions = {
    takeDown: (site) => setAsking({ kind: "takedown", site }),
    restore: (s) => void restore(s, s.taken_down_reason || "Taken down again"),
    suspend: (user) => setAsking({ kind: "suspend", user }),
    enable: (u) => void enable(u, u.suspended_reason || "Suspended again"),
    makeAccount: (email) => run("POST", "/v1/admin/people", { email }, "Account made. They sign in with that email."),
  };

  return (
    <ActionsContext.Provider value={actions}>
      {children}
      <AskDialog
        open={asking?.kind === "takedown"}
        onOpenChange={(o) => !o && setAsking(null)}
        title={asking?.kind === "takedown" ? "Take down " + asking.site.name + "?" : ""}
        body='Every address shows "This site has been taken down" and changes are refused. Restore puts it back. The owner sees your reason.'
        label="Reason, in one line"
        confirmLabel="Take down"
        danger
        onConfirm={async (reason) => {
          if (asking?.kind === "takedown") await takeDown(asking.site, reason);
        }}
      />
      <AskDialog
        open={asking?.kind === "suspend"}
        onOpenChange={(o) => !o && setAsking(null)}
        title={asking?.kind === "suspend" ? "Suspend " + who(asking.user) + "?" : ""}
        body="Their keys, connected apps and sign-in stop working and all their sites are taken down. Re-enable reverses it. The owner sees your reason."
        label="Reason, in one line"
        confirmLabel="Suspend"
        danger
        onConfirm={async (reason) => {
          if (asking?.kind === "suspend") await suspend(asking.user, reason);
        }}
      />
    </ActionsContext.Provider>
  );
}
