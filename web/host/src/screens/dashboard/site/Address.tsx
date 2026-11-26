// Address: a free short name (name.simple-host.app, at once) and the site's
// own domain (two DNS records at the registrar, then HTTPS by itself). The
// site's own address keeps working either way.
import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Badge } from "@kit/components/ui/badge";
import { Button } from "@kit/components/ui/button";
import { CopyButton } from "@kit/components/ui/copy";
import { AskDialog } from "@kit/components/ui/dialog";
import { FieldMessage, Input, Label } from "@kit/components/ui/input";
import { host, useChange, type DomainState, type Site } from "../data";
import { Part } from "../bits";

const shortDomain = () => window.location.hostname.replace(/^www\./, "");

export function Address({ site }: { site: Site }) {
  return (
    <>
      <Part title="The site's own address" note="Every site has this one. It always works, whatever you add below.">
        <p className="flex flex-wrap items-center gap-2">
          <a href={site.url} target="_blank" rel="noopener" className="inline-flex min-h-11 items-center break-all">
            {host(site.url)}
          </a>
          <CopyButton text={site.url} variant="ghost" className="px-3" />
        </p>
      </Part>
      <Short site={site} />
      <Domain site={site} />
    </>
  );
}

function Short({ site }: { site: Site }) {
  const change = useChange(site);
  const [name, setName] = useState("");
  const [error, setError] = useState("");
  const [asking, setAsking] = useState(false);
  const url = "https://" + site.short_name + "." + shortDomain() + "/";
  return (
    <Part title="A free short address" note={"A shorter name for this site, like bakery." + shortDomain() + ". It works at once, with no setup."}>
      {site.short_name ? (
        <>
          <p className="flex flex-wrap items-center gap-2">
            <a href={url} target="_blank" rel="noopener" className="inline-flex min-h-11 items-center font-semibold break-all">
              {host(url)}
            </a>
            <CopyButton text={url} variant="ghost" className="px-3" />
            <Button variant="ghost" className="px-3 text-bad" onClick={() => setAsking(true)}>
              Let it go
            </Button>
          </p>
          <AskDialog
            open={asking}
            onOpenChange={setAsking}
            title={"Let go of " + host(url) + "?"}
            body="Links to the short address stop working, and someone else may take the name. The site's own address keeps working."
            confirmLabel="Let it go"
            danger
            onConfirm={() => change({ short_name: "" }, "The short address is gone. The site's own address still works.")}
          />
        </>
      ) : (
        <form
          noValidate
          onSubmit={async (e) => {
            e.preventDefault();
            const n = name.trim().toLowerCase();
            if (!/^[a-z0-9](?:[a-z0-9]|-(?=[a-z0-9])){0,62}$/.test(n)) return setError("Use lower-case letters, digits and single hyphens.");
            setError("");
            if (await change({ short_name: n }, "The site now also answers at " + n + "." + shortDomain() + ".")) setName("");
          }}
        >
          <Label htmlFor="short-name">Short name</Label>
          <div className="flex flex-wrap items-center gap-2">
            <div className="flex min-w-0 flex-[1_1_16rem] items-center gap-1.5">
              <Input id="short-name" value={name} onChange={(e) => (setName(e.target.value), setError(""))} autoComplete="off" autoCapitalize="none" spellCheck={false} aria-invalid={!!error} placeholder={site.name} />
              <span className="flex-none text-ink-soft">.{shortDomain()}</span>
            </div>
            <Button type="submit">Get it</Button>
          </div>
          {error && <FieldMessage tone="bad">{error}</FieldMessage>}
        </form>
      )}
    </Part>
  );
}

const STATUS: Record<string, [string, "ok" | "accent" | "neutral" | "down"]> = {
  live: ["live", "ok"],
  certificate: ["HTTPS on its way", "accent"],
  waiting: ["waiting for DNS", "neutral"],
};

/** DomainStatus is a domain's state and, until it is live, the records it needs. */
export function DomainStatus({ d, onCheck }: { d: DomainState; onCheck?: () => void }) {
  const [word, tone] = STATUS[d.status] || [d.status, "neutral"];
  return (
    <div>
      <p className="flex flex-wrap items-center gap-2">
        <strong className="break-all">{d.host}</strong> <Badge tone={tone}>{word}</Badge>
      </p>
      {d.says && <p className="mt-1 text-sm text-ink-soft">{d.says}</p>}
      {d.error && <FieldMessage tone="bad">{d.error}</FieldMessage>}
      {d.status !== "live" && !!d.records?.length && (
        <>
          <p className="mt-3 text-sm">Add these at the company where you bought the domain:</p>
          <ul className="m-0 mt-2 grid list-none gap-2 p-0">
            {d.records.map((r) => (
              <li key={r.type + r.host} className="rounded-field border border-rule bg-card px-3 py-2.5">
                <p className="flex flex-wrap items-center gap-2 text-sm">
                  <strong>{r.type}</strong> record
                  {r.ok ? <Badge tone="ok">found</Badge> : <Badge tone={r.optional ? "neutral" : "down"}>{r.optional ? "optional" : "not found yet"}</Badge>}
                </p>
                <dl className="mt-1 grid grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-x-3 gap-y-1 text-sm">
                  <dt className="text-ink-soft">Name</dt>
                  <dd className="m-0 font-mono break-all">{r.host}</dd>
                  <dd className="m-0"><CopyButton text={r.host} variant="ghost" className="px-3 text-sm" aria-label={"Copy the name of the " + r.type + " record"} /></dd>
                  <dt className="text-ink-soft">Value</dt>
                  <dd className="m-0 font-mono break-all">{r.value}</dd>
                  <dd className="m-0"><CopyButton text={r.value} variant="ghost" className="px-3 text-sm" aria-label={"Copy the value of the " + r.type + " record"} /></dd>
                </dl>
              </li>
            ))}
          </ul>
          {onCheck && (
            <Button className="mt-3" onClick={onCheck}>
              Check again
            </Button>
          )}
        </>
      )}
    </div>
  );
}

function Domain({ site }: { site: Site }) {
  const change = useChange(site);
  const qc = useQueryClient();
  const [domain, setDomain] = useState("");
  const [error, setError] = useState("");
  const [asking, setAsking] = useState(false);
  const d = site.domain;
  return (
    <Part title="Your own domain" note="Use a domain you own, like www.your-bakery.com. You add two records where you bought it; HTTPS follows by itself.">
      {d?.host ? (
        <>
          <DomainStatus d={d} onCheck={() => qc.invalidateQueries({ queryKey: ["site", site.owner, site.name] })} />
          <Button variant="ghost" className="mt-3 px-3 text-bad" onClick={() => setAsking(true)}>
            Remove the domain
          </Button>
          <AskDialog
            open={asking}
            onOpenChange={setAsking}
            title={"Remove " + d.host + "?"}
            body="The domain stops showing this site. The site's own address keeps working."
            confirmLabel="Remove"
            danger
            onConfirm={() => change({ domain: "" }, d.host + " no longer shows this site.")}
          />
        </>
      ) : (
        <form
          noValidate
          onSubmit={async (e) => {
            e.preventDefault();
            const h = domain.trim().toLowerCase().replace(/^https?:\/\//, "").replace(/\/.*$/, "");
            if (!/^[a-z0-9-]+(\.[a-z0-9-]+)+$/.test(h)) return setError("Type the domain alone, like www.example.com.");
            setError("");
            if (await change({ domain: h }, "Connected " + h + ". Now add the records shown.")) setDomain("");
          }}
        >
          <Label htmlFor="domain">Domain</Label>
          <div className="flex flex-wrap gap-2">
            <Input id="domain" className="flex-[1_1_16rem]" value={domain} onChange={(e) => (setDomain(e.target.value), setError(""))} placeholder="www.example.com" autoComplete="off" autoCapitalize="none" spellCheck={false} aria-invalid={!!error} />
            <Button type="submit">Connect</Button>
          </div>
          {error && <FieldMessage tone="bad">{error}</FieldMessage>}
        </form>
      )}
    </Part>
  );
}
