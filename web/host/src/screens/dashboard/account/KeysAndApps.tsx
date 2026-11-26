// API keys (use Simple Host without a connector; a new one is shown once) and the AI apps connected to
// the account. Revoking a key or disconnecting an app asks first: whatever
// uses it stops at once and cannot be put back.
import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useApi } from "@/lib/api";
import { Button } from "@kit/components/ui/button";
import { CopyButton } from "@kit/components/ui/copy";
import { AskDialog } from "@kit/components/ui/dialog";
import { FieldMessage, Input, Label } from "@kit/components/ui/input";
import { useToast } from "@kit/components/ui/toast";
import type { Me } from "../data";
import { Ago, Part, Row } from "../bits";

type Asking = { kind: "key" | "app"; id: string; name: string } | null;

export function KeysAndApps({ me }: { me: Me }) {
  const call = useApi();
  const qc = useQueryClient();
  const toast = useToast();
  const [name, setName] = useState("");
  const [made, setMade] = useState<string>("");
  const [error, setError] = useState("");
  const [asking, setAsking] = useState<Asking>(null);

  return (
    <>
      <Part title="Connected AI apps" note="The apps that act for you: they can do what you can do here." className="mt-2">
        {me.apps.length ? (
          <ul className="m-0 list-none p-0">
            {me.apps.map((a) => (
              <Row
                key={a.id}
                end={
                  <Button variant="ghost" className="px-3 text-bad" onClick={() => setAsking({ kind: "app", id: a.id, name: a.name || "this app" })}>
                    Disconnect
                  </Button>
                }
              >
                <p className="font-semibold">{a.name || "AI app"}</p>
                <p className="text-sm text-ink-soft">
                  Connected <Ago at={a.connected_at} />
                  {a.last_used_at && (
                    <>
                      {" · last used "}
                      <Ago at={a.last_used_at} />
                    </>
                  )}
                </p>
              </Row>
            ))}
          </ul>
        ) : (
          <p className="text-ink-soft">
            No apps yet. <a href="/install">Connect one</a>: it takes a minute.
          </p>
        )}
      </Part>

      <Part title="Keys" note="API key: use Simple Host without a connector. Send it as X-API-Key: <key>. A key acts as you, so keep it like a password.">
        {me.keys.length ? (
          <ul className="m-0 mb-4 list-none p-0">
            {me.keys.map((k) => (
              <Row
                key={k.id}
                end={
                  <Button variant="ghost" className="px-3 text-bad" onClick={() => setAsking({ kind: "key", id: k.id, name: k.name || "this key" })}>
                    Revoke
                  </Button>
                }
              >
                <p className="font-semibold">
                  {k.name || "Key"} <span className="font-mono text-sm font-normal text-ink-soft">…{k.last4}</span>
                </p>
                <p className="text-sm text-ink-soft">{k.last_used_at ? <>Last used <Ago at={k.last_used_at} /></> : "Not used yet"}</p>
              </Row>
            ))}
          </ul>
        ) : (
          <p className="mb-4 text-ink-soft">No keys.</p>
        )}
        {made && (
          <div role="status" className="mb-4 rounded-field border-[1.5px] border-accent bg-accent-soft px-4 py-3">
            <p className="font-semibold">Your new key. It is shown only this once.</p>
            <div className="mt-2 flex flex-wrap items-center gap-2">
              <code className="min-w-0 flex-[1_1_16rem] rounded-field border border-rule bg-card px-3 py-2.5 font-mono text-sm break-all">{made}</code>
              <CopyButton text={made} />
            </div>
          </div>
        )}
        <form
          noValidate
          onSubmit={async (e) => {
            e.preventDefault();
            if (!name.trim()) return setError("Say what it is for, so you know which to revoke later.");
            setError("");
            try {
              const r = await call<{ key: string }>("POST", "/v1/keys", { name: name.trim() });
              setMade(r.key);
              setName("");
              await qc.invalidateQueries({ queryKey: ["me"] });
            } catch (err) {
              setError((err as Error).message);
            }
          }}
        >
          <Label htmlFor="key-name">New key, for</Label>
          <div className="flex flex-wrap gap-2">
            <Input id="key-name" className="flex-[1_1_16rem]" value={name} onChange={(e) => (setName(e.target.value), setError(""))} placeholder="my deploy script" autoComplete="off" aria-invalid={!!error} />
            <Button type="submit" variant="primary" className="px-5">
              Make key
            </Button>
          </div>
          {error && <FieldMessage tone="bad">{error}</FieldMessage>}
        </form>
      </Part>

      <AskDialog
        open={!!asking}
        onOpenChange={(o) => !o && setAsking(null)}
        title={asking?.kind === "key" ? "Revoke " + asking.name + "?" : "Disconnect " + (asking?.name || "") + "?"}
        body={
          asking?.kind === "key"
            ? "Anything that uses it stops working at once. A revoked key cannot come back; make a new one if you need it."
            : "It can no longer see or change your sites. You can connect it again later."
        }
        confirmLabel={asking?.kind === "key" ? "Revoke" : "Disconnect"}
        danger
        onConfirm={async () => {
          if (!asking) return;
          try {
            await call("DELETE", asking.kind === "key" ? "/v1/keys/" + encodeURIComponent(asking.id) : "/v1/me/apps/" + encodeURIComponent(asking.id));
            await qc.invalidateQueries({ queryKey: ["me"] });
            toast(asking.kind === "key" ? "Revoked " + asking.name + "." : "Disconnected " + asking.name + ".");
          } catch (e) {
            toast((e as Error).message, { bad: true });
          }
        }}
      />
    </>
  );
}
