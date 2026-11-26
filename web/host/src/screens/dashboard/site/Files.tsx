// Files and versions: send new or changed files (the one filled button),
// add photos through the photo page, and make an older kept version live
// again (with Undo, so it never asks first).
import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useApi } from "@/lib/api";
import { dateOf } from "@kit/lib/format";
import { Badge } from "@kit/components/ui/badge";
import { Button } from "@kit/components/ui/button";
import { Check } from "@kit/components/ui/choice";
import { FieldMessage } from "@kit/components/ui/input";
import { useToast } from "@kit/components/ui/toast";
import { publish, sitePath, useChange, useMe, type Site } from "../data";
import { Ago, Part, Row } from "../bits";
import { FilePick } from "../FilePick";

export function Files({ site }: { site: Site }) {
  const me = useMe().data!;
  return (
    <>
      <Update site={site} />
      <Photos site={site} />
      <Versions site={site} kept={me.use.kept_versions} />
    </>
  );
}

function Update({ site }: { site: Site }) {
  const me = useMe().data!;
  const call = useApi();
  const qc = useQueryClient();
  const toast = useToast();
  const [files, setFiles] = useState<File[]>([]);
  const [keep, setKeep] = useState(true);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  return (
    <Part title="Update the site" note="Most changes are easier to ask your AI app for. To do it yourself, choose the new or changed files here.">
      <form
        noValidate
        onSubmit={async (e) => {
          e.preventDefault();
          if (!files.length) return setError("Choose at least one file, or a folder.");
          setError("");
          setBusy(true);
          try {
            await publish(call, site, me, files, keep);
            await qc.invalidateQueries({ queryKey: ["site", site.owner, site.name] });
            setFiles([]);
            toast("Published. The new version is live.");
          } catch (err) {
            setError((err as Error).message);
          } finally {
            setBusy(false);
          }
        }}
      >
        <FilePick files={files} onChange={(f) => (setFiles(f), setError(""))} />
        <Check checked={keep} onChange={setKeep} hint={keep ? "Files you do not send stay as they are." : "Files you do not send are removed from the new version."}>
          Keep my other files
        </Check>
        <div className="mt-3">
          <Button type="submit" variant="primary" className="px-6" disabled={busy || site.taken_down}>
            {busy ? "Publishing…" : "Publish"}
          </Button>
        </div>
        {error && <FieldMessage tone="bad">{error}</FieldMessage>}
      </form>
    </Part>
  );
}

/** Photos opens the photo page for this site: photos are shrunk on the device and saved to its files. */
function Photos({ site }: { site: Site }) {
  const me = useMe().data!;
  const call = useApi();
  const toast = useToast();
  const [busy, setBusy] = useState(false);
  const files = site.namespaces.find((n) => n.tables.includes("_files"))?.name || site.namespaces[0]?.name || "photos";
  return (
    <Part title="Add photos" note={"Photos are made smaller on your device, with camera and location details removed, then saved to the site's “" + files + "” files."}>
      <Button
        disabled={busy || site.taken_down}
        onClick={async () => {
          setBusy(true);
          try {
            const r = await call<{ url: string }>("POST", sitePath(site, me, "/upload-links"), { kind: "files", namespace: files });
            window.location.assign(r.url);
          } catch (e) {
            toast((e as Error).message, { bad: true });
            setBusy(false);
          }
        }}
      >
        Add photos
      </Button>
    </Part>
  );
}

function Versions({ site, kept }: { site: Site; kept: number }) {
  const change = useChange(site);
  const list = [...site.versions].sort((a, b) => b.n - a.n);
  const live = (n: number, back?: number) =>
    change({ live_version: n }, "Version " + n + " is live.", back ? () => void live(back) : undefined);
  return (
    <Part title="Versions" note={"Each publish is a version. The newest " + kept + " are kept; making an older one live again is instant."}>
      {list.length ? (
        <ul className="m-0 list-none p-0">
          {list.map((v) => (
            <Row
              key={v.n}
              end={
                v.n === site.live_version ? (
                  <Badge tone="ok">live</Badge>
                ) : (
                  <Button onClick={() => live(v.n, site.live_version)} disabled={site.taken_down}>
                    Make live
                  </Button>
                )
              }
            >
              <p className="font-semibold">Version {v.n}</p>
              <p className="text-sm text-ink-soft">
                {dateOf(v.at)} · <Ago at={v.at} />
                {v.author ? " · by " + v.author : ""}
              </p>
            </Row>
          ))}
        </ul>
      ) : (
        <p className="text-ink-soft">No versions yet.</p>
      )}
    </Part>
  );
}
