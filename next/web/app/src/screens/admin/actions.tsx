// The operator's actions: take a site down or restore it, suspend an account
// or re-enable it, make an account (self-hosted). Each reloads the lists.
//   PATCH /v1/sites/{site}?owner=<handle>  {taken_down, reason}
//   PATCH /v1/admin/people/{id}            {suspended, reason}
//   POST  /v1/admin/people                 {email}
import { createContext, useContext, useState, type ReactNode } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useApi } from "@/lib/api";
import { AskDialog } from "@/components/ui/dialog";
import { useToast } from "@/components/ui/toast";
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

  const run = async (method: string, path: string, body: unknown, done: string) => {
    try {
      const b = await call<{ says?: string }>(method, path, body);
      await qc.invalidateQueries({ queryKey: ["admin"] });
      toast(b.says || done);
      return true;
    } catch (e) {
      toast((e as Error).message, { bad: true });
      return false;
    }
  };
  const sitePath = (s: Site) => "/v1/sites/" + encodeURIComponent(s.name) + "?owner=" + encodeURIComponent(s.owner);
  const who = (u: Person) => u.handle || u.email;

  const actions: Actions = {
    takeDown: (site) => setAsking({ kind: "takedown", site }),
    restore: (s) => void run("PATCH", sitePath(s), { taken_down: false }, "Restored " + s.name + "."),
    suspend: (user) => setAsking({ kind: "suspend", user }),
    enable: (u) => void run("PATCH", "/v1/admin/people/" + encodeURIComponent(u.id), { suspended: false }, "Re-enabled " + who(u) + "."),
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
          if (asking?.kind === "takedown")
            await run("PATCH", sitePath(asking.site), { taken_down: true, reason }, "Took down " + asking.site.name + ".");
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
          if (asking?.kind === "suspend")
            await run("PATCH", "/v1/admin/people/" + encodeURIComponent(asking.user.id), { suspended: true, reason }, "Suspended " + who(asking.user) + ".");
        }}
      />
    </ActionsContext.Provider>
  );
}
