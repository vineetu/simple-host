// Website: what visitors get at the event's address. The built-in page or
// the organiser's own site (a zip). Switching to your own site asks first;
// an upload while the built-in page shows asks first too (it goes live at
// once). Read-only once ended (the server refuses then) or under inspection.
import { useState, type DragEvent } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Button } from "@kit/components/ui/button";
import { RadioCards } from "@kit/components/ui/choice";
import { CopyButton } from "@kit/components/ui/copy";
import { AskDialog } from "@kit/components/ui/dialog";
import { Notice } from "@kit/components/ui/notice";
import { Skeleton } from "@kit/components/ui/skeleton";
import { useToast } from "@kit/components/ui/toast";
import { cn } from "@kit/lib/utils";
import { paths, useHack } from "@/lib/client";
import { dateTime } from "@/lib/time";
import { errorText, uploadBytes } from "./common";
import { activeVersion, archiveErrorText, type Website as Site } from "./eventpage-data";
import { EndedReadOnly, FilePick, Pill, SavedTick, TabHead, useSavedTick, type TabProps } from "./eventpage-ui";

const box = "mt-4 max-w-prose rounded-field border-[1.5px] border-rule bg-card px-4 py-4 md:px-5";

export function Website(p: TabProps) {
  const { slug, ev, admin, ended, website } = p;
  const api = useHack();
  const qc = useQueryClient();
  const toast = useToast();
  const [tick, flash] = useSavedTick();
  const [ask, setAsk] = useState<null | { kind: "mode" } | { kind: "upload"; file: File }>(null);
  const [busy, setBusy] = useState(false);
  const [over, setOver] = useState(false);
  const readOnly = admin || ended;
  const url = String(ev.url || "");
  const w: Site = website.data || { mode: String(ev.website_mode || "builtin"), versions: [] };
  const customLive = w.mode === "custom";
  const versions = w.versions || [];
  const hasUploads = versions.length > 0;
  const active = activeVersion(w);
  const activeLine = active ? `Version ${active.version_number}, uploaded ${dateTime(active.created_at, ev, { dateOnly: true })}.` : "";

  const setWebsite = (next: Site) => qc.setQueryData(["website", slug], next);
  const setMode = async (mode: "builtin" | "custom") => {
    setBusy(true);
    try {
      setWebsite((await api.content.setWebsite(slug, { mode })) as Site);
      void qc.invalidateQueries({ queryKey: ["event", slug] });
      flash();
    } catch (e) {
      toast(errorText(e), { bad: true });
    } finally {
      setBusy(false);
    }
  };
  const upload = async (file: File) => {
    setBusy(true);
    try {
      // The event's site is the event account's site named by the event id; ?create=1 makes it on the first upload.
      const site = w.site || String(ev.id || p.d.organiser?.website_site || "");
      await uploadBytes("PUT", paths.siteUpload(site, slug), file, "application/zip");
    } catch (e) {
      setBusy(false);
      return toast(archiveErrorText(e), { bad: true });
    }
    try {
      await api.content.setWebsite(slug, { mode: "custom" });
      const got = (await api.content.website(slug)) as Site;
      setWebsite(got);
      void qc.invalidateQueries({ queryKey: ["event", slug] });
      const latest = (got.versions || []).slice(-1)[0];
      toast(latest ? `Version ${latest.version_number} is up` : "Website published");
    } catch (e) {
      toast(errorText(e), { bad: true });
    } finally {
      setBusy(false);
    }
  };
  const chosen = (file: File) => (customLive ? void upload(file) : setAsk({ kind: "upload", file }));
  const drop = (e: DragEvent) => {
    e.preventDefault();
    setOver(false);
    const f = e.dataTransfer.files?.[0];
    if (f && !readOnly && !busy) chosen(f);
  };

  return (
    <>
      <TabHead title="Website" sub="What visitors get at your address." />
      {ended && !admin && <EndedReadOnly what="Your website" />}
      {customLive && <Notice title="Your own website is live at your address.">{readOnly ? null : "Basics, About and Look still save, and show again if you switch back to the built-in page."}</Notice>}
      {website.isError && (
        <Notice
          tone="bad"
          title="Your website settings could not be loaded."
          action={
            <Button onClick={() => website.refetch()} disabled={website.isFetching}>
              Reload
            </Button>
          }
        >
          Reload to edit them.
        </Notice>
      )}
      <section aria-label="Your address" className={box}>
        <h3 className="type-label text-ink-soft">Your address</h3>
        <div className="mt-1.5 flex flex-wrap items-center gap-2">
          <span className="min-w-0 flex-[1_1_14rem] font-mono text-sm break-all">{url.replace(/^https?:\/\//, "")}</span>
          <CopyButton text={url} />
          <Button asChild>
            <a href={url} target="_blank" rel="noopener noreferrer">
              Open <span className="sr-only">(opens in a new tab)</span>
            </a>
          </Button>
        </div>
        <div className="mt-5 flex flex-wrap items-center gap-x-3">
          <h3 className="type-section text-base">What shows there</h3>
          {!readOnly && <SavedTick on={tick} />}
        </div>
        {website.isPending ? (
          <Skeleton className="mt-3 h-32 w-full" />
        ) : readOnly ? (
          <p className="mt-1 text-ink-soft">{customLive ? `Your own website. ${activeLine}` : "Built-in event page."}</p>
        ) : (
          <div className={cn("mt-3", busy && "pointer-events-none opacity-70")}>
            <RadioCards
              label="What shows at your address"
              value={customLive ? "custom" : "builtin"}
              onChange={(v) => (v === "custom" ? setAsk({ kind: "mode" }) : void setMode("builtin"))}
              options={[
                { value: "builtin", title: "Built-in event page", hint: "Made from Basics, About, Schedule, Tracks, Prizes, Rules, Sponsors and FAQ. It updates as you edit." },
                ...(hasUploads ? [{ value: "custom" as const, title: "Your own website", hint: `${activeLine} You can switch back any time. Nothing is deleted.` }] : []),
              ]}
            />
            {!hasUploads && (
              <div className="mt-2.5 rounded-field border-[1.5px] border-dashed border-rule px-4 py-3 text-ink-soft" aria-disabled="true">
                <span className="block">Your own website</span>
                <span className="mt-0.5 block text-sm">Upload a zip first</span>
              </div>
            )}
          </div>
        )}
      </section>

      <section aria-label="Your own website" className={box}>
        <h3 className="type-section">Your own website</h3>
        <p className="mt-0.5 text-sm text-ink-soft">
          A .zip with index.html at the top, up to 25 MB.{" "}
          {hasUploads ? "Each upload goes live at your address right away. Earlier uploads stay listed." : "Your first upload goes live at your address right away."}
          {w.upload_limits ? " " + w.upload_limits : ""}
        </p>
        {!readOnly && (
          <div
            onDragOver={(e) => (e.preventDefault(), setOver(true))}
            onDragLeave={() => setOver(false)}
            onDrop={drop}
            className={cn("mt-3 flex flex-col items-center gap-2 rounded-field border-[1.5px] border-dashed px-4 py-5 text-center", over ? "border-accent bg-accent-soft" : "border-ink-soft")}
          >
            <span className="text-ink">{busy ? "Uploading…" : "Drop a .zip here"}</span>
            <span className="text-sm text-ink-soft">or</span>
            <FilePick primary label="Choose zip" accept=".zip,application/zip" onFile={chosen} disabled={busy || website.isPending} />
          </div>
        )}
        <h4 className="mt-5 type-label text-ink-soft">Uploads</h4>
        {versions.length ? (
          <ul aria-label="Uploads" className="mt-1.5 list-none">
            {versions
              .slice()
              .reverse()
              .map((v, i) => (
                <li key={v.version_number} className="flex min-h-12 items-center gap-3 border-b border-rule py-1.5 last:border-b-0">
                  <span className="min-w-0 flex-1">
                    <span className="block text-ink">Version {v.version_number}</span>
                    <span className="block text-sm text-ink-soft">{dateTime(v.created_at, ev)}</span>
                  </span>
                  {customLive && v.version_number === active?.version_number ? <Pill tone="ok">Live</Pill> : !customLive && i === 0 ? <Pill>Newest</Pill> : null}
                </li>
              ))}
          </ul>
        ) : (
          <p className="mt-1.5 text-ink-soft">No uploads yet.</p>
        )}
        <p className="mt-4 text-sm text-ink-soft">An AI app can publish versions here too, and manage your website&apos;s own saved data (keys, tables and files).</p>
        <p className="mt-2 text-sm text-ink-soft">
          Your own website updates itself at each stage, results included, when it is built from the “event” page recipe. Made yours earlier? Ask your AI app to add the live event feed from that recipe.
        </p>
      </section>

      <AskDialog
        open={ask?.kind === "mode"}
        onOpenChange={(o) => !o && setAsk(null)}
        title="Switch to custom website?"
        body="Your own site replaces the built-in page at this address. You can switch back any time. Nothing is deleted."
        confirmLabel="Switch"
        onConfirm={() => setMode("custom")}
      />
      <AskDialog
        open={ask?.kind === "upload"}
        onOpenChange={(o) => !o && setAsk(null)}
        title="Switch to your own website?"
        body="This upload replaces the built-in page at your address right away. You can switch back to the built-in page any time."
        confirmLabel="Upload and go live"
        onConfirm={() => (ask?.kind === "upload" ? upload(ask.file) : undefined)}
      />
    </>
  );
}
