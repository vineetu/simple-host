// A new site from files on this device: whose (me or a team), its name, and
// the files or a whole folder. Most people ask their AI app instead.
import { useState } from "react";
import { Link, useNavigate } from "react-router";
import { useQueryClient } from "@tanstack/react-query";
import { useApi } from "@/lib/api";
import { Button } from "@kit/components/ui/button";
import { Card, CardLead, CardTitle } from "@kit/components/ui/card";
import { Field, FieldMessage, Input, NativeSelect } from "@kit/components/ui/input";
import { useToast } from "@kit/components/ui/toast";
import { pagePath, publish, useMe, validName } from "./data";
import { FilePick } from "./FilePick";

export function NewSite() {
  const me = useMe().data!;
  const call = useApi();
  const qc = useQueryClient();
  const toast = useToast();
  const navigate = useNavigate();
  const [owner, setOwner] = useState(me.handle);
  const [name, setName] = useState("");
  const [files, setFiles] = useState<File[]>([]);
  const [errors, setErrors] = useState<{ name?: string; files?: string; send?: string }>({});
  const [busy, setBusy] = useState(false);

  return (
    <div className="pt-6 pb-10">
      <Link to="/dashboard" className="inline-flex min-h-11 items-center text-sm">
        ← Your sites
      </Link>
      <div className="mt-2 flex justify-center">
        <Card className="max-w-[560px]">
          <CardTitle>New site</CardTitle>
          <CardLead>
            Choose your site's files, with an <code className="font-mono text-sm">index.html</code> for its first page. It goes live at once,
            and anyone with the link can open it until you change that.
          </CardLead>
          <form
            noValidate
            onSubmit={async (e) => {
              e.preventDefault();
              const n = name.trim().toLowerCase();
              const bad = {
                name: !n ? "Give the site a name." : !validName(n) ? "Use lower-case letters, digits and single hyphens, like bakery or spring-menu." : undefined,
                files: files.length ? undefined : "Choose at least one file, or a folder.",
              };
              setErrors(bad);
              if (bad.name || bad.files) return;
              setBusy(true);
              try {
                await publish(call, { name: n, owner }, me, files, false);
                await qc.invalidateQueries({ queryKey: ["sites"] });
                toast(n + " is live.");
                navigate(pagePath({ name: n, owner }));
              } catch (err) {
                setErrors({ send: (err as Error).message });
              } finally {
                setBusy(false);
              }
            }}
          >
            {me.teams.length > 0 && (
              <Field id="owner" label="Whose site">
                <NativeSelect id="owner" className="w-full" value={owner} onChange={(e) => setOwner(e.target.value)}>
                  <option value={me.handle}>Mine ({me.handle})</option>
                  {me.teams.map((t) => (
                    <option key={t.handle} value={t.handle}>
                      The team {t.display_name || t.handle}
                    </option>
                  ))}
                </NativeSelect>
              </Field>
            )}
            <Field id="name" label="Site name" error={errors.name} hint="Lower-case letters, digits and single hyphens. It is part of the address.">
              <Input
                id="name"
                value={name}
                onChange={(e) => setName(e.target.value)}
                autoComplete="off"
                autoCapitalize="none"
                spellCheck={false}
                maxLength={63}
                placeholder="bakery"
                aria-invalid={!!errors.name}
                aria-describedby="name-msg"
              />
            </Field>
            <FilePick files={files} onChange={setFiles} error={errors.files} />
            <Button type="submit" variant="primary" size="lg" disabled={busy}>
              {busy ? "Publishing…" : "Publish"}
            </Button>
            {errors.send && <FieldMessage tone="bad">{errors.send}</FieldMessage>}
          </form>
        </Card>
      </div>
    </div>
  );
}
