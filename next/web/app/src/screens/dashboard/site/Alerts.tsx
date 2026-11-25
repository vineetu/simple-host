// Alerts: what about this site the owner is told, in their AI app and as a
// push on the devices they switched on (Account › Notifications).
import { useState } from "react";
import { Link } from "react-router";
import { Button } from "@/components/ui/button";
import { Check } from "@/components/ui/choice";
import { useChange, useMe, type Rule, type Site } from "../data";
import { Part } from "../bits";

type Target = { ns: string; target: string };

export function Alerts({ site }: { site: Site }) {
  const me = useMe().data!;
  const change = useChange(site);
  const has = (r: Rule) => site.notifications.some((n) => n.kind === r.kind && (n.namespace || "") === (r.namespace || "") && (n.target || "") === (r.target || ""));
  const targets: Target[] = site.namespaces.flatMap((n) => {
    const own = n.tables.filter((t) => !t.startsWith("_"));
    const list = [...own, ...n.tables.filter((t) => t === "_values" || t === "_files")];
    return (list.length ? list : ["_values", "_files"]).map((target) => ({ ns: n.name, target }));
  });
  const [ask, setAsk] = useState(has({ kind: "access" }));
  const [on, setOn] = useState<Record<string, boolean>>(() =>
    Object.fromEntries(targets.map((t) => [t.ns + "/" + t.target, has({ kind: "data", namespace: t.ns, target: t.target })])),
  );
  const [busy, setBusy] = useState(false);
  const words = (t: Target) => (t.target === "_values" ? "values in " + t.ns : t.target === "_files" ? "files in " + t.ns : t.ns === t.target ? t.ns : t.target + " in " + t.ns);

  return (
    <>
      <Part
        title="Tell me when"
        note={
          <>
            Alerts come to your AI app, and as a push on the devices you switched on in{" "}
            <Link to="/dashboard/account#notifications">Account › Notifications</Link>
            {me.devices.length ? " (" + me.devices.length + " now)" : " (none yet)"}.
          </>
        }
      >
        <Check checked={ask} onChange={setAsk} hint={site.access === "viewers" ? undefined : "Visitors can ask only while the site is for named people."}>
          Someone asks to open the site
        </Check>
        {targets.map((t) => (
          <Check key={t.ns + "/" + t.target} checked={!!on[t.ns + "/" + t.target]} onChange={(v) => setOn({ ...on, [t.ns + "/" + t.target]: v })}>
            Something is added or changed: {words(t)}
          </Check>
        ))}
        {!targets.length && <p className="py-2 text-sm text-ink-soft">When the site saves data, you can be told about new entries here too.</p>}
        <Button
          variant="primary"
          className="mt-3 px-6"
          disabled={busy}
          onClick={async () => {
            setBusy(true);
            const list: Rule[] = targets.filter((t) => on[t.ns + "/" + t.target]).map((t) => ({ kind: "data", namespace: t.ns, target: t.target }));
            if (ask) list.push({ kind: "access" });
            await change({ notifications: list }, list.length ? "Saved. You will be told." : "Saved. No alerts for this site.");
            setBusy(false);
          }}
        >
          Save
        </Button>
      </Part>
    </>
  );
}
