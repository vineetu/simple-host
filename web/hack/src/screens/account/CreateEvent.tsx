// Create event (/events/new): the least needed to make a draft event (title
// first, which fills in the address with a live availability check; then
// dates, time zone, organiser, contact, purpose, size). Everything stays
// filled through a refusal; an address refusal is said under the address.
// Created, it opens the event's organiser Overview.
import { useEffect, useMemo, useRef, useState } from "react";
import { Button } from "@kit/components/ui/button";
import { Field, FieldMessage, Input, NativeSelect, Textarea } from "@kit/components/ui/input";
import { PageTitle } from "@kit/components/ui/page-shell";
import { useHack } from "@/lib/client";
import { useGo } from "@/app/nav";
import { browserZone, createErrors, errText, NAME_CODES, site, slugify, zoneOptions, type CreateForm } from "./data";

type Check = { text: string; tone?: "bad" | "good" };

export default function CreateEvent() {
  const api = useHack();
  const go = useGo();
  const tz = useMemo(browserZone, []);
  const zones = useMemo(() => zoneOptions(tz), [tz]);
  const [f, setF] = useState<CreateForm>({ title: "", slug: "", start: "", end: "", tz, organiser: "", org: "", email: "", purpose: "", n: "60" });
  const [errs, setErrs] = useState<Partial<Record<keyof CreateForm, string>>>({});
  const [formErr, setFormErr] = useState("");
  const [check, setCheck] = useState<Check>({ text: "yourname." + site.host() });
  const [busy, setBusy] = useState(false);
  const touched = useRef(false);
  const seq = useRef(0);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  useEffect(() => () => clearTimeout(timer.current), []);

  const checkSoon = (slug: string) => {
    clearTimeout(timer.current);
    timer.current = setTimeout(async () => {
      const s = slug.trim().toLowerCase();
      if (!s) return setCheck({ text: "yourname." + site.host() });
      const mine = ++seq.current;
      setCheck({ text: "Checking…" });
      try {
        const r = await api.hack.nameFree(s);
        if (mine !== seq.current) return;
        if (r?.available) setCheck({ text: s + "." + site.host() + " · available", tone: "good" });
        else setCheck({ text: errText({ code: r?.code, message: r?.error }, "That address is taken."), tone: "bad" });
      } catch (e) {
        if (mine !== seq.current) return;
        const status = (e as { status?: number }).status;
        setCheck({ text: status === 0 ? "Could not connect. Check your connection and try again." : errText(e, "Could not check that address."), tone: "bad" });
      }
    }, 400);
  };

  const set = (k: keyof CreateForm) => (e: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement>) => {
    const v = e.target.value;
    setErrs((x) => ({ ...x, [k]: undefined }));
    if (k === "title") {
      const slug = touched.current ? f.slug : slugify(v);
      setF({ ...f, title: v, slug });
      checkSoon(slug);
    } else if (k === "slug") {
      touched.current = true;
      setF({ ...f, slug: v.toLowerCase() });
      checkSoon(v.toLowerCase());
    } else setF({ ...f, [k]: v });
  };

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setFormErr("");
    const bad = createErrors(f);
    setErrs(bad);
    const first = Object.keys(bad)[0];
    if (first) {
      document.getElementById("new-" + first)?.focus();
      return;
    }
    setBusy(true);
    try {
      const r = await api.hack.createEvent({
        slug: f.slug.trim().toLowerCase(),
        title: f.title.trim(),
        organiser_name: f.organiser.trim(),
        organisation: f.org.trim(),
        contact_email: f.email.trim(),
        purpose: f.purpose.trim(),
        expected_participants: Number(f.n.trim()),
        starts_at: f.start,
        time_zone: f.tz,
        ...(f.end ? { ends_at: f.end } : {}),
      });
      const slug = r?.event?.slug || f.slug.trim().toLowerCase();
      go("/e/" + encodeURIComponent(slug) + "/manage");
    } catch (e) {
      const code = (e as { code?: string }).code || "";
      const status = (e as { status?: number }).status;
      if (status === 401) return;
      if (NAME_CODES.includes(code)) {
        setCheck({ text: errText(e), tone: "bad" });
        document.getElementById("new-slug")?.focus();
      } else setFormErr(status === 0 ? "Could not connect. Check your connection and try again." : errText(e));
      setBusy(false);
    }
  };

  const field = (k: keyof CreateForm) => ({ id: "new-" + k, value: f[k], onChange: set(k), "aria-invalid": !!errs[k], "aria-describedby": "new-" + k + "-msg" });

  return (
    <>
      <div className="flex flex-wrap items-end gap-3">
        <div className="min-w-0 flex-1">
          <PageTitle sub="Set up a draft event. You can edit the details later.">Create event</PageTitle>
        </div>
        <Button type="submit" form="new-form" variant="primary" disabled={busy}>
          Create event
        </Button>
      </div>
      <form id="new-form" noValidate onSubmit={submit} className="mt-7 max-w-prose">
        <Field id="new-title" label="Title" error={errs.title}>
          <Input {...field("title")} maxLength={120} />
        </Field>
        <div className="mb-4">
          <Field id="new-slug" label="Address" error={errs.slug}>
            <Input {...field("slug")} maxLength={39} autoCapitalize="off" spellCheck={false} aria-describedby="new-slug-msg new-slug-check" />
          </Field>
          {!errs.slug && (
            <div className="-mt-3">
              <FieldMessage tone={check.tone} id="new-slug-check">
                {check.text}
              </FieldMessage>
            </div>
          )}
        </div>
        <div className="grid gap-x-4 md:grid-cols-2">
          <Field id="new-start" label="Starts" error={errs.start}>
            <Input {...field("start")} type="date" />
          </Field>
          <Field id="new-end" label="Ends (optional)" error={errs.end}>
            <Input {...field("end")} type="date" />
          </Field>
        </div>
        <Field id="new-tz" label="Time zone" hint="Every deadline and schedule time shows in this zone.">
          <NativeSelect id="new-tz" value={f.tz} onChange={set("tz")} className="h-12 w-full" aria-describedby="new-tz-msg">
            {zones.map((z) => (
              <option key={z.zone} value={z.zone}>
                {z.label}
              </option>
            ))}
          </NativeSelect>
        </Field>
        <Field id="new-organiser" label="Your name" error={errs.organiser}>
          <Input {...field("organiser")} maxLength={100} autoComplete="name" />
        </Field>
        <Field id="new-org" label="Organisation (optional)" error={errs.org}>
          <Input {...field("org")} maxLength={120} placeholder="Company, college or group, optional" />
        </Field>
        <Field id="new-email" label="Contact email" error={errs.email}>
          <Input {...field("email")} type="email" autoComplete="email" />
        </Field>
        <Field id="new-purpose" label="Purpose" error={errs.purpose}>
          <Textarea {...field("purpose")} maxLength={1000} />
        </Field>
        <Field id="new-n" label="Expected participants" error={errs.n}>
          <Input {...field("n")} type="number" min={1} max={100000} inputMode="numeric" className="max-w-48" />
        </Field>
        {formErr && <FieldMessage tone="bad">{formErr}</FieldMessage>}
        <Button type="submit" className="mt-3" disabled={busy}>
          Create event
        </Button>
      </form>
    </>
  );
}
