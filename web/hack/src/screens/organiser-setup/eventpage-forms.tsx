// Basics and About: the two form tabs, each one Save with Discard and an
// unsaved-changes line. Both stay editable after the event ends (PATCH on
// the event is not blocked then); only an admin inspection is read-only.
import { useMemo, useState, type FormEvent } from "react";
import { Field, Input, Label, Textarea } from "@kit/components/ui/input";
import { useToast } from "@kit/components/ui/toast";
import { useHack } from "@/lib/client";
import { dateTime, isLocalMidnight } from "@/lib/time";
import { boundaryInput, boundaryValue, Counter, DateTimePair, errorText, SaveFoot, useRefreshEvent, ZoneSelect, type Ev } from "./common";
import { CustomSiteNote, EndedEditable, TabHead, type TabProps } from "./eventpage-ui";
import { useUnsaved } from "@kit/guard/unsaved";

const boundaryDate = (iso: string | null | undefined, ev: Ev) => (iso ? dateTime(iso, ev, { dateOnly: isLocalMidnight(iso, ev) }) : "—");

function ReadOnlyList({ rows }: { rows: [string, string][] }) {
  return (
    <dl className="mt-4 max-w-prose rounded-field border-[1.5px] border-rule bg-card px-4 py-3">
      {rows.map(([k, v]) => (
        <div key={k} className="border-b border-rule py-2 last:border-b-0">
          <dt className="type-label text-ink-soft">{k}</dt>
          <dd className="whitespace-pre-line">{v || "—"}</dd>
        </div>
      ))}
    </dl>
  );
}

const card = "mt-4 max-w-prose rounded-field border-[1.5px] border-rule bg-card px-4 pt-4 pb-1 md:px-5";

// -------------------------------------------------------------------- Basics

type BasicsForm = { title: string; tagline: string; sd: string; st: string; ed: string; et: string; tz: string };

function basicsOf(ev: Ev): BasicsForm {
  const s = boundaryInput(ev.starts_at, ev.time_zone);
  const e = boundaryInput(ev.ends_at, ev.time_zone);
  return { title: ev.title, tagline: String(ev.tagline || ""), sd: s.date, st: s.time, ed: e.date, et: e.time, tz: ev.time_zone || "UTC" };
}
const same = <T extends object>(a: T, b: T) => (Object.keys(a) as (keyof T)[]).every((k) => a[k] === b[k]);

export function Basics({ slug, ev, admin, ended, website }: TabProps) {
  const api = useHack();
  const toast = useToast();
  const refresh = useRefreshEvent(slug);
  const saved = basicsOf(ev);
  const [f, setF] = useState(saved);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [titleErr, setTitleErr] = useState("");
  const dirty = !same(f, saved);
  useUnsaved(dirty);
  const head = <TabHead title="Basics" sub="The name, tagline and dates at the top of your page." />;
  // The zone list is long (every zone, each with its name and offset): built again only when the zone changes.
  const zone = useMemo(() => <ZoneSelect id="f-tz" value={f.tz} onChange={(z) => setF((o) => ({ ...o, tz: z }))} />, [f.tz]);

  if (admin)
    return (
      <>
        {head}
        <CustomSiteNote website={website.data} readOnly />
        <ReadOnlyList
          rows={[
            ["Title", ev.title],
            ["Tagline", String(ev.tagline || "")],
            ["Starts", boundaryDate(ev.starts_at, ev)],
            ["Ends", boundaryDate(ev.ends_at, ev)],
            ["Time zone", ev.time_zone || ""],
          ]}
        />
      </>
    );

  const set = (k: keyof BasicsForm) => (v: string) => setF((o) => ({ ...o, [k]: v }));
  const save = async (e: FormEvent) => {
    e.preventDefault();
    setError("");
    if (!f.title.trim()) {
      setTitleErr("Give the event a title.");
      return;
    }
    setTitleErr("");
    const body: Record<string, unknown> = { title: f.title, tagline: f.tagline, time_zone: f.tz };
    // An unchanged boundary is left out, so a time-zone change keeps the server's re-anchoring.
    if (f.sd !== saved.sd || f.st !== saved.st) body.starts_at = boundaryValue(f.sd, f.st);
    if (f.ed !== saved.ed || f.et !== saved.et) body.ends_at = boundaryValue(f.ed, f.et);
    setBusy(true);
    try {
      await api.event.update(slug, body);
      await refresh();
      toast("Saved");
    } catch (err) {
      setError(errorText(err));
    } finally {
      setBusy(false);
    }
  };
  return (
    <>
      {head}
      <CustomSiteNote website={website.data} readOnly={false} />
      {ended && <EndedEditable />}
      <form className={card} aria-label="Basics" onSubmit={save} noValidate>
        <Field id="f-title" label="Title" error={titleErr}>
          <Input id="f-title" maxLength={120} value={f.title} onChange={(e) => set("title")(e.target.value)} aria-invalid={!!titleErr} aria-describedby="f-title-msg" />
        </Field>
        <div className="mb-4">
          <div className="flex items-baseline justify-between gap-2">
            <Label htmlFor="f-tag">Tagline (optional)</Label>
            <Counter n={f.tagline.length} max={160} />
          </div>
          <Input id="f-tag" maxLength={160} value={f.tagline} onChange={(e) => set("tagline")(e.target.value)} aria-describedby="f-tag-msg" />
          <p id="f-tag-msg" className="mt-1.5 text-sm text-ink-soft">
            One line under the title. Link previews in WhatsApp show it too.
          </p>
        </div>
        <div>
          <DateTimePair id="f-start" label="Starts" date={f.sd} time={f.st} onDate={set("sd")} onTime={set("st")} />
          <DateTimePair id="f-end" label="Ends" date={f.ed} time={f.et} onDate={set("ed")} onTime={set("et")} />
        </div>
        <Field id="f-tz" label="Time zone" hint="Every deadline and schedule time shows in this zone.">
          {zone}
        </Field>
        <SaveFoot dirty={dirty} busy={busy} error={error} onDiscard={() => (setF(saved), setTitleErr(""), setError(""))} />
      </form>
    </>
  );
}

// --------------------------------------------------------------------- About

type AboutForm = { about: string; prizes: string; p1: string; p2: string; p3: string; rules: string; coc: string };

function aboutOf(ev: Ev): AboutForm {
  const op = (ev.overall_prizes as string[] | undefined) || [];
  return { about: String(ev.about || ""), prizes: String(ev.prizes || ""), p1: op[0] || "", p2: op[1] || "", p3: op[2] || "", rules: String(ev.rules || ""), coc: String(ev.coc_text || "") };
}

export function About({ slug, ev, admin, ended, website }: TabProps) {
  const api = useHack();
  const toast = useToast();
  const refresh = useRefreshEvent(slug);
  const saved = aboutOf(ev);
  const [f, setF] = useState(saved);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const dirty = !same(f, saved);
  useUnsaved(dirty);
  const head = <TabHead title="About" sub="The longer text on your page. A blank line starts a new paragraph." />;

  if (admin)
    return (
      <>
        {head}
        <CustomSiteNote website={website.data} readOnly />
        <ReadOnlyList
          rows={[
            ["About", saved.about],
            ["Prizes", saved.prizes],
            ["Prize for 1st / 2nd / 3rd", [saved.p1, saved.p2, saved.p3].map((p) => p || "—").join(" / ")],
            ["Rules", saved.rules],
            ["Code of conduct additions", saved.coc],
          ]}
        />
      </>
    );

  const set = (k: keyof AboutForm) => (v: string) => setF((o) => ({ ...o, [k]: v }));
  const save = async (e: FormEvent) => {
    e.preventDefault();
    setError("");
    setBusy(true);
    try {
      await api.event.update(slug, { overall_prizes: [f.p1, f.p2, f.p3], about: f.about, prizes: f.prizes, rules: f.rules, coc_text: f.coc });
      await refresh();
      toast("Saved");
    } catch (err) {
      setError(errorText(err));
    } finally {
      setBusy(false);
    }
  };
  return (
    <>
      {head}
      <CustomSiteNote website={website.data} readOnly={false} />
      {ended && <EndedEditable />}
      <form className={card} aria-label="About" onSubmit={save}>
        <div className="mb-4">
          <div className="flex items-baseline justify-between gap-2">
            <Label htmlFor="f-about">About</Label>
            <Counter n={f.about.length} max={10000} />
          </div>
          <Textarea id="f-about" rows={7} maxLength={10000} value={f.about} onChange={(e) => set("about")(e.target.value)} />
        </div>
        <Field id="f-prizes" label="Prizes (optional)">
          <Textarea id="f-prizes" rows={3} maxLength={5000} value={f.prizes} onChange={(e) => set("prizes")(e.target.value)} />
        </Field>
        <div className="mb-4" role="group" aria-labelledby="f-places-l">
          <Label id="f-places-l" htmlFor="f-prize-1">
            Prize for 1st / 2nd / 3rd (optional)
          </Label>
          <div className="grid gap-2 md:grid-cols-3">
            <Input id="f-prize-1" aria-label="Prize for 1st" maxLength={60} placeholder="$300" value={f.p1} onChange={(e) => set("p1")(e.target.value)} />
            <Input aria-label="Prize for 2nd" maxLength={60} value={f.p2} onChange={(e) => set("p2")(e.target.value)} />
            <Input aria-label="Prize for 3rd" maxLength={60} value={f.p3} onChange={(e) => set("p3")(e.target.value)} />
          </div>
        </div>
        <Field id="f-rules" label="Rules (optional)">
          <Textarea id="f-rules" rows={3} maxLength={10000} value={f.rules} onChange={(e) => set("rules")(e.target.value)} />
        </Field>
        <h3 className="mt-6 type-section">Code of conduct</h3>
        <details className="mt-1 mb-3">
          <summary className="flex min-h-11 cursor-pointer items-center text-sm text-ink-soft">Read the standard code, which every event shows first</summary>
          <p className="text-sm whitespace-pre-line text-ink-soft">{String(ev.coc_default || "")}</p>
        </details>
        <Field id="f-coc" label="Your additions (optional)" hint="Shown after the standard code on the join page.">
          <Textarea id="f-coc" rows={3} maxLength={10000} value={f.coc} onChange={(e) => set("coc")(e.target.value)} aria-describedby="f-coc-msg" />
        </Field>
        <SaveFoot sticky dirty={dirty} busy={busy} error={error} onDiscard={() => (setF(saved), setError(""))} />
      </form>
    </>
  );
}
