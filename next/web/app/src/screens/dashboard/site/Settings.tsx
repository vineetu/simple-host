// Settings: the public page listing, offline and back (with Undo), a copy of
// everything, renaming, and, apart at the bottom, deleting (to the 7-day bin)
// and, for the operator, taking down.
import { useState } from "react";
import { useNavigate } from "react-router";
import { useQueryClient } from "@tanstack/react-query";
import { useApi } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Check } from "@/components/ui/choice";
import { AskDialog } from "@/components/ui/dialog";
import { useToast } from "@/components/ui/toast";
import { sitePath, useChange, useMe, type Site } from "../data";
import { Part } from "../bits";

export function Settings({ site }: { site: Site }) {
  const me = useMe().data!;
  const call = useApi();
  const qc = useQueryClient();
  const toast = useToast();
  const navigate = useNavigate();
  const change = useChange(site);
  const [asking, setAsking] = useState<"" | "delete" | "takedown">("");
  const offline = (v: boolean) =>
    change({ offline: v }, v ? site.name + " is offline." : site.name + " is back online.", () => void change({ offline: !v }, v ? site.name + " is back online." : site.name + " is offline."));

  return (
    <>
      <Part title="Public page" note="Your public page lists the sites you choose, at your own address.">
        <Check
          checked={site.listed}
          onChange={(v) => change({ listed: v }, v ? site.name + " is on your public page." : site.name + " is off your public page.")}
          disabled={site.taken_down}
        >
          List {site.name} on my public page
        </Check>
      </Part>

      <Part
        title={site.offline ? "Offline" : "Take offline"}
        note={
          site.offline
            ? "Visitors see “This site is offline” and cannot save anything. Its files, versions and saved data are kept."
            : "For a pause: visitors see “This site is offline” instead of the site. Everything is kept, and putting it back is instant."
        }
      >
        <Button onClick={() => offline(!site.offline)} disabled={site.taken_down}>
          {site.offline ? "Put back online" : "Take offline"}
        </Button>
      </Part>

      <Part title="Download a copy" note="The download holds every site of yours with its files and saved data, and your account details, as one .zip. It comes as a link by email.">
        <Button
          onClick={async () => {
            try {
              const r = await call<{ says?: string }>("POST", "/v1/me/export", {});
              toast(r.says || "We are making it. You will get an email with a link when it is ready.");
            } catch (e) {
              toast((e as Error).message, { bad: true });
            }
          }}
        >
          Email me a download
        </Button>
      </Part>

      <Part title="Rename" note={"A site's name is part of its address, so the service does not rename sites in place. To move it, publish the same files under the new name (New site), connect any short address or domain there, then delete " + site.name + ". Saved data stays with the old name."} />

      <Part
        danger
        title="Delete this site"
        className="mt-12"
        note={"Every address of " + site.name + " stops working. It stays in Recently deleted for 7 days with its saved data, and you can restore it from there."}
      >
        <div className="flex flex-wrap gap-2">
          <Button variant="danger" onClick={() => setAsking("delete")}>
            Delete {site.name}
          </Button>
          {me.operator && (
            <Button variant="danger" onClick={() => (site.taken_down ? change({ taken_down: false }, "Restored " + site.name + ".") : setAsking("takedown"))}>
              {site.taken_down ? "Restore (operator)" : "Take down (operator)"}
            </Button>
          )}
        </div>
      </Part>

      <AskDialog
        open={asking === "delete"}
        onOpenChange={(o) => !o && setAsking("")}
        title={"Delete " + site.name + "?"}
        body="It goes to Recently deleted for 7 days, with its saved data, then is removed for good. You can restore it until then."
        confirmLabel="Delete site"
        danger
        onConfirm={async () => {
          try {
            await call("DELETE", sitePath(site, me));
            await qc.invalidateQueries({ queryKey: ["sites"] });
            qc.removeQueries({ queryKey: ["site", site.owner, site.name] });
            navigate("/dashboard");
            toast("Deleted " + site.name + ". It is in Recently deleted for 7 days.", {
              undo: {
                label: "Undo",
                run: async () => {
                  try {
                    await call("PATCH", sitePath(site, me), { restore: true });
                    await qc.invalidateQueries({ queryKey: ["sites"] });
                    toast("Restored " + site.name + ".");
                  } catch (e) {
                    toast((e as Error).message, { bad: true });
                  }
                },
              },
            });
          } catch (e) {
            toast((e as Error).message, { bad: true });
          }
        }}
      />
      <AskDialog
        open={asking === "takedown"}
        onOpenChange={(o) => !o && setAsking("")}
        title={"Take down " + site.name + "?"}
        body='Every address shows "This site has been taken down" and changes are refused. Restore puts it back. The owner sees your reason.'
        label="Reason, in one line"
        confirmLabel="Take down"
        danger
        onConfirm={(reason) => change({ taken_down: true, reason }, "Took down " + site.name + ".")}
      />
    </>
  );
}
