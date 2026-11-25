// Profile: the name sites live under (asked first: every address moves),
// the display name, what the public page shows, one domain for all sites,
// and what the account may hold.
import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useApi } from "@/lib/api";
import { bytes, plural } from "@/lib/format";
import { Button } from "@/components/ui/button";
import { AskDialog } from "@/components/ui/dialog";
import { Field, FieldMessage, Input, NativeSelect } from "@/components/ui/input";
import { useToast } from "@/components/ui/toast";
import { useSites, validName, type Me } from "../data";
import { Part } from "../bits";
import { DomainStatus } from "../site/Address";

export function Profile({ me }: { me: Me }) {
  const call = useApi();
  const qc = useQueryClient();
  const toast = useToast();
  const sites = useSites().data || [];
  const mine = sites.filter((s) => s.owner === me.handle);
  const [handle, setHandle] = useState(me.handle);
  const [name, setName] = useState(me.display_name || "");
  const [home, setHome] = useState(me.home_site || "");
  const [domain, setDomain] = useState(me.domain || "");
  const [errors, setErrors] = useState<{ handle?: string; domain?: string; send?: string }>({});
  const [asking, setAsking] = useState(false);
  const [busy, setBusy] = useState(false);

  const body = () => {
    const b: Record<string, string> = {};
    if (name !== (me.display_name || "")) b.display_name = name;
    if (home !== (me.home_site || "")) b.home_site = home;
    const d = domain.trim().toLowerCase();
    if (d !== (me.domain || "")) b.domain = d;
    const h = handle.trim().toLowerCase();
    if (h !== me.handle) b.handle = h;
    return b;
  };
  const save = async () => {
    setBusy(true);
    try {
      await call("PATCH", "/v1/owners/" + encodeURIComponent(me.id), body());
      await qc.invalidateQueries();
      toast("Saved.");
    } catch (e) {
      setErrors({ send: (e as Error).message });
    } finally {
      setBusy(false);
    }
  };
  const u = me.use;

  return (
    <form
      noValidate
      onSubmit={(e) => {
        e.preventDefault();
        const h = handle.trim().toLowerCase();
        const d = domain.trim().toLowerCase();
        const bad = {
          handle: !validName(h) ? "Use lower-case letters, digits and single hyphens." : undefined,
          domain: d && !/^[a-z0-9-]+(\.[a-z0-9-]+)+$/.test(d) ? "Type the domain alone, like example.com." : undefined,
        };
        setErrors(bad);
        if (bad.handle || bad.domain) return;
        if (!Object.keys(body()).length) return toast("Nothing to save: nothing has changed.");
        if (h !== me.handle) return setAsking(true);
        void save();
      }}
    >
      <Part title="Your name on Simple Host" className="mt-2">
        <Field id="handle" label="Name" error={errors.handle} hint="Every site's address has it in. Changing it moves every site at once, and old links stop working.">
          <Input id="handle" value={handle} onChange={(e) => setHandle(e.target.value)} autoComplete="username" autoCapitalize="none" spellCheck={false} maxLength={40} aria-invalid={!!errors.handle} aria-describedby="handle-msg" />
        </Field>
        <Field id="display" label="Display name" hint="Shown on your public page.">
          <Input id="display" value={name} onChange={(e) => setName(e.target.value)} autoComplete="name" />
        </Field>
      </Part>
      <Part title="Your public page" note="Your own page lists the sites you switched on in their Settings. Or one site can be the page instead.">
        <Field id="home" label="The page shows">
          <NativeSelect id="home" className="w-full md:w-auto md:min-w-80" value={home} onChange={(e) => setHome(e.target.value)}>
            <option value="">The list of my sites</option>
            {mine.map((s) => (
              <option key={s.name} value={s.name}>
                The site {s.name}
              </option>
            ))}
          </NativeSelect>
        </Field>
      </Part>
      <Part title="One domain for all your sites" note="Point *.yourdomain at Simple Host once, and every site answers at site.yourdomain.">
        <Field id="all-domain" label="Domain" error={errors.domain}>
          <Input id="all-domain" value={domain} onChange={(e) => setDomain(e.target.value)} placeholder="example.com" autoComplete="off" autoCapitalize="none" spellCheck={false} aria-invalid={!!errors.domain} aria-describedby="all-domain-msg" />
        </Field>
        {me.domain_status && <DomainStatus d={me.domain_status} onCheck={() => qc.invalidateQueries({ queryKey: ["me"] })} />}
      </Part>
      <div className="mt-6">
        <Button type="submit" variant="primary" className="px-6" disabled={busy}>
          {busy ? "Saving…" : "Save"}
        </Button>
        {errors.send && <FieldMessage tone="bad">{errors.send}</FieldMessage>}
      </div>
      <Part title="What your account holds">
        <p className="text-ink-soft">
          {u.unlimited
            ? plural(u.sites, "site") + ". This account has no limits."
            : plural(u.sites, "site") +
              ". Each site may hold " +
              bytes(u.site_bytes_limit) +
              " of files and " +
              bytes(u.data_bytes_limit) +
              " of saved data, and each file store " +
              bytes(u.files_bytes_limit) +
              ". The newest " +
              u.kept_versions +
              " versions of each site are kept."}
        </p>
      </Part>
      <AskDialog
        open={asking}
        onOpenChange={setAsking}
        title={"Change your name to " + handle.trim().toLowerCase() + "?"}
        body={"Every site moves to addresses under the new name at once. Links with " + me.handle + " stop working, and nobody can use " + me.handle + " again, you included."}
        confirmLabel="Change name"
        danger
        onConfirm={save}
      />
    </form>
  );
}
