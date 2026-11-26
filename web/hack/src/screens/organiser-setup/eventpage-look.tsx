// Look: page style, icon, colour, and whether entries and submissions show.
// Each control saves on change with a "Saved" tick beside it. Colour, style
// and the two switches keep working after End; the icon does not (the
// server refuses PUT/DELETE E/icon once archived). An inspection is read-only.
import { useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Button } from "@kit/components/ui/button";
import { Check, RadioCards } from "@kit/components/ui/choice";
import { useToast } from "@kit/components/ui/toast";
import { cn } from "@kit/lib/utils";
import { paths, useHack } from "@/lib/client";
import { markColours } from "@/lib/labels";
import { errorText, uploadBytes } from "./common";
import { anotherTheme, checkImage, CRAYONS, LOGO_TYPES, SWATCH_HEX, THEMES } from "./eventpage-data";
import { EndedEditable, FilePick, SavedTick, TabHead, useSavedTick, type TabProps } from "./eventpage-ui";

const box = "mt-4 max-w-prose rounded-field border-[1.5px] border-rule bg-card px-4 py-4 md:px-5";

function SettingHead({ title, tick, children }: { title: string; tick?: boolean; children: React.ReactNode }) {
  return (
    <div className="min-w-0">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <h3 className="type-section text-base">{title}</h3>
        {tick !== undefined && <SavedTick on={tick} />}
      </div>
      <p className="mt-0.5 text-sm text-ink-soft">{children}</p>
    </div>
  );
}

/** Swatches is the colour choice: one round crayon per colour, the whole swatch and its name the target. */
function Swatches({ value, auto, onChange, disabled }: { value: string; auto: string; onChange: (v: string) => void; disabled: boolean }) {
  return (
    <fieldset className="mt-3 flex flex-wrap gap-x-2 gap-y-1" aria-label="Event colour">
      {CRAYONS.map(([v, l]) => {
        const hex = v ? SWATCH_HEX[v] : auto;
        const on = value === v;
        return (
          <label key={v || "auto"} className={cn("relative flex min-h-11 min-w-16 cursor-pointer flex-col items-center gap-1 rounded-field px-1.5 py-1.5", disabled && "cursor-default opacity-60")}>
            <input type="radio" name="colour" value={v} checked={on} disabled={disabled} onChange={() => onChange(v)} className="peer absolute inset-0 size-full cursor-pointer appearance-none rounded-field" />
            <span
              aria-hidden="true"
              className={cn("grid size-8 place-items-center rounded-tag border-[1.5px]", on ? "border-ink" : "border-rule", !v && "border-dashed")}
              style={{ background: hex, color: markColours(hex).ink }}
            >
              {on && (
                <svg viewBox="0 0 20 20" className="size-4" fill="none" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round" strokeLinejoin="round">
                  <path d="M4 10.5l4 4 8-9" />
                </svg>
              )}
            </span>
            <span className={cn("text-xs", on ? "text-ink" : "text-ink-soft")}>{l}</span>
          </label>
        );
      })}
    </fieldset>
  );
}

export function Look(p: TabProps) {
  const { slug, ev, admin, ended } = p;
  const api = useHack();
  const qc = useQueryClient();
  const toast = useToast();
  const refresh = () => qc.invalidateQueries({ queryKey: ["event", slug] });
  const [theme, setTheme] = useState(String(ev.theme || ""));
  const [colour, setColour] = useState(String(ev.accent_color || ""));
  const [gallery, setGallery] = useState(!!ev.gallery_open);
  const [subs, setSubs] = useState(!!ev.submissions_public);
  const [busyIcon, setBusyIcon] = useState(false);
  const [themeTick, flashTheme] = useSavedTick();
  const [colourTick, flashColour] = useSavedTick();
  const [galleryTick, flashGallery] = useSavedTick();
  const [subsTick, flashSubs] = useSavedTick();
  // Picks are written one after another, so a quick second pick lands after the first; only the latest pick repaints.
  const chain = useRef(Promise.resolve());
  const lastTheme = useRef(theme);
  const lastColour = useRef(colour);
  const savedTheme = useRef(theme);
  const savedColour = useRef(colour);
  const iconLocked = ended || admin;

  const auto = String(ev.color || "#d15d48");
  const hex = colour ? SWATCH_HEX[colour] || auto : auto;
  const hasIcon = !!ev.icon_media_type;

  const pickTheme = (v: string) => {
    lastTheme.current = v;
    setTheme(v);
    chain.current = chain.current.then(async () => {
      try {
        await api.event.update(slug, { theme: v });
        savedTheme.current = v;
        if (v === lastTheme.current) flashTheme();
      } catch (e) {
        toast(errorText(e), { bad: true });
        if (v === lastTheme.current) setTheme(savedTheme.current);
      }
    });
  };
  const pickColour = (v: string) => {
    lastColour.current = v;
    setColour(v);
    chain.current = chain.current.then(async () => {
      try {
        await api.event.update(slug, { accent_color: v });
        savedColour.current = v;
        if (v === lastColour.current) {
          flashColour();
          void refresh();
        }
      } catch (e) {
        toast(errorText(e), { bad: true });
        if (v === lastColour.current) setColour(savedColour.current);
      }
    });
  };
  const toggle = async (field: "gallery_open" | "submissions_public", want: boolean, set: (v: boolean) => void, flash: () => void) => {
    set(want);
    try {
      await api.event.update(slug, { [field]: want });
      flash();
    } catch (e) {
      set(!want);
      toast(errorText(e), { bad: true });
    }
  };
  const upload = async (file: File) => {
    const bad = checkImage(file);
    if (bad) return toast(bad.replace("Logo", "The icon"), { bad: true });
    setBusyIcon(true);
    try {
      await uploadBytes("PUT", paths.icon(slug), file, file.type);
      await refresh();
    } catch (e) {
      toast(errorText(e), { bad: true });
    } finally {
      setBusyIcon(false);
    }
  };
  const removeIcon = async () => {
    setBusyIcon(true);
    try {
      await api.content.removeIcon(slug);
      await refresh();
    } catch (e) {
      toast(errorText(e), { bad: true });
    } finally {
      setBusyIcon(false);
    }
  };
  const themeName = THEMES.find(([v]) => v === theme)?.[1] || "Classic";
  const { bg, ink } = markColours(/^#[0-9a-f]{6}$/i.test(hex) ? hex : "#d15d48");

  return (
    <>
      <TabHead title="Look" sub="Your icon, colour, page style and whether entries show. Each change saves straight away." />
      {ended && !admin && <EndedEditable />}
      <section aria-label="Preview" className={box}>
        <h3 className="type-label text-ink-soft">Preview</h3>
        <div className="mt-2 flex items-center gap-3 rounded-field px-4 py-4" style={{ background: bg, color: ink }}>
          <span className="grid size-13 flex-none place-items-center overflow-hidden rounded-tag border-2 bg-card" style={{ borderColor: ink }}>
            {hasIcon && <img src={String(ev.icon_url)} alt="" width={52} height={52} className="size-full object-cover" />}
          </span>
          <span className="min-w-0">
            <span className="block text-lg font-semibold">{ev.title}</span>
            {ev.tagline ? <span className="block text-sm">{String(ev.tagline)}</span> : null}
          </span>
        </div>
        <p className="mt-2 text-sm text-ink-soft">Page style: {themeName}</p>
      </section>

      <section aria-label="Page style" className={box}>
        <div className="flex flex-wrap items-start justify-between gap-2">
          <SettingHead title="Page style" tick={themeTick}>
            Background, type and card shapes for your public page.
          </SettingHead>
          {!admin && <Button onClick={() => pickTheme(anotherTheme(theme))}>Try another</Button>}
        </div>
        <div className={cn("mt-3", admin && "pointer-events-none opacity-60")} aria-disabled={admin || undefined}>
          <RadioCards label="Page style" value={theme || "classic"} onChange={(v) => !admin && pickTheme(v === "classic" ? "" : v)} options={THEMES.map(([v, t, h]) => ({ value: v || "classic", title: t, hint: h }))} />
        </div>
      </section>

      <section aria-label="Event icon" className={box}>
        <div className="flex flex-wrap items-center gap-x-4 gap-y-3">
          <div className="min-w-0 flex-[1_1_16rem]">
            <SettingHead title="Event icon">
              Shown round on your page, in Find a hackathon and on join links. PNG, JPEG or WebP, up to 128 KB.{ended ? " Can’t change after End." : ""}
            </SettingHead>
          </div>
          <span className="grid size-11 flex-none place-items-center overflow-hidden rounded-tag border-[1.5px] border-rule bg-paper-lo">
            {hasIcon && <img src={String(ev.icon_url)} alt="The event icon" className="size-full object-cover" />}
          </span>
          {!iconLocked && (
            <div className="flex flex-none flex-wrap gap-2">
              <FilePick label={hasIcon ? "Replace" : "Upload icon"} accept={LOGO_TYPES.join(",")} onFile={upload} disabled={busyIcon} />
              {hasIcon && (
                <Button onClick={removeIcon} disabled={busyIcon} aria-label="Remove event icon">
                  Remove
                </Button>
              )}
            </div>
          )}
        </div>
      </section>

      <section aria-label="Colour" className={box}>
        <SettingHead title="Colour" tick={colourTick}>
          For buttons, links and your page style&apos;s highlights. Automatic picks one from your address.
        </SettingHead>
        <Swatches value={colour} auto={auto} onChange={pickColour} disabled={admin} />
      </section>

      <section aria-label="What shows on the page" className={box}>
        <SettingHead title="What shows on the page">Entries and submissions on your public page.</SettingHead>
        <div className="mt-2 flex items-start gap-3">
          <div className="min-w-0 flex-1">
            <Check checked={gallery} disabled={admin} onChange={(v) => toggle("gallery_open", v, setGallery, flashGallery)} hint="Lists every team whose website is live. People's choice voting needs this on.">
              Show entries on the event page
            </Check>
          </div>
          <span className="pt-2.5">
            <SavedTick on={galleryTick} />
          </span>
        </div>
        <div className="flex items-start gap-3 border-t border-rule">
          <div className="min-w-0 flex-1">
            <Check checked={subs} disabled={admin} onChange={(v) => toggle("submissions_public", v, setSubs, flashSubs)} hint="From Judging on, anyone can see each submitted entry.">
              Show submissions on the event page
            </Check>
          </div>
          <span className="pt-2.5">
            <SavedTick on={subsTick} />
          </span>
        </div>
      </section>
    </>
  );
}
