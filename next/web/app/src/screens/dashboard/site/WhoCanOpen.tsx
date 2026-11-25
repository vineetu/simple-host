// Who can open the site: anyone with the link, people with the passcode, or
// only people named by email. Picking a choice shows what it needs; Save
// switches. While a choice is in force, its passcode or list is changed here
// directly (removing a person offers Undo).
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { RadioCards } from "@/components/ui/choice";
import { CopyButton } from "@/components/ui/copy";
import { AskDialog } from "@/components/ui/dialog";
import { Field, FieldMessage, Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import { ACCESS_WORDS, useChange, type Access, type Site } from "../data";
import { Row } from "../bits";

const okCode = (c: string) => c.trim().length >= 6 && c.trim().length <= 128;

export function WhoCanOpen({ site }: { site: Site }) {
  const change = useChange(site);
  const [mode, setMode] = useState<Access>(site.access);
  const [code, setCode] = useState("");
  const [codeError, setCodeError] = useState("");
  const [asking, setAsking] = useState(false);
  const [busy, setBusy] = useState(false);
  const [draft, setDraft] = useState<string[]>(site.viewers.map((v) => v.email));
  const switching = mode !== site.access;

  const save = async () => {
    if (mode === "passcode" && !site.passcode && !okCode(code)) return setCodeError("Use a passcode of 6 to 128 characters.");
    if (mode === "open") return setAsking(true);
    setBusy(true);
    const body = mode === "passcode" ? (code ? { passcode: code.trim() } : { access: "passcode" }) : { access: "viewers", viewers: draft };
    if (await change(body, "Saved. " + ACCESS_WORDS[mode] + " can open " + site.name + ".")) setCode("");
    setBusy(false);
  };

  return (
    <>
      <h2 className="type-section">Who can open {site.name}</h2>
      <p className="mt-1 mb-4 max-w-[70ch] text-sm text-ink-soft">
        This covers every address of the site, its pages and its saved data. You, and your team on a team site, can always open it.
      </p>
      <RadioCards
        label="Who can open the site"
        value={mode}
        onChange={(v) => (setMode(v), setCodeError(""))}
        options={[
          { value: "open", title: "Anyone with the link", hint: "No sign-in, no passcode." },
          {
            value: "passcode",
            title: "People with the passcode",
            hint: "Visitors type it once on each device.",
            more:
              site.access === "passcode" && site.passcode ? (
                <Passcode site={site} />
              ) : (
                <NewCode value={code} onChange={(v) => (setCode(v), setCodeError(""))} error={codeError} />
              ),
          },
          {
            value: "viewers",
            title: "Only people I name",
            hint: "They sign in with their email address. Nobody else gets in.",
            more: site.access === "viewers" ? <Named site={site} /> : <Draft list={draft} onChange={setDraft} />,
          },
        ]}
      />
      {switching && (
        <div className="mt-5 flex flex-wrap items-center gap-3">
          <Button variant="primary" className="px-6" onClick={save} disabled={busy}>
            Save
          </Button>
          <Button variant="link" className="min-h-11 px-2" onClick={() => (setMode(site.access), setCode(""), setCodeError(""))}>
            Keep it as it is ({ACCESS_WORDS[site.access].toLowerCase()})
          </Button>
        </div>
      )}
      <AskDialog
        open={asking}
        onOpenChange={setAsking}
        title={"Open " + site.name + " to anyone?"}
        body="Anyone who has the link can then see every page and whatever the pages show of the saved data. The passcode and the list of people are kept, in case you switch back."
        confirmLabel="Open to anyone"
        onConfirm={() => change({ access: "open" }, site.name + " is open to anyone with the link.")}
      />
    </>
  );
}

function NewCode({ value, onChange, error }: { value: string; onChange: (v: string) => void; error: string }) {
  return (
    <Field id="new-passcode" label="Passcode" error={error} hint="6 to 128 characters. Share it with the people you want in.">
      <Input
        id="new-passcode"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        autoComplete="off"
        spellCheck={false}
        aria-invalid={!!error}
        aria-describedby="new-passcode-msg"
      />
    </Field>
  );
}

/** Passcode is the one in force: see it, copy it (with the link), change it, sign everyone out. */
function Passcode({ site }: { site: Site }) {
  const change = useChange(site);
  const [shown, setShown] = useState(false);
  const [code, setCode] = useState("");
  const [error, setError] = useState("");
  return (
    <>
      <p className="type-label text-ink-soft">The passcode</p>
      <div className="mt-1 flex flex-wrap items-center gap-2">
        <code className="min-w-0 flex-[1_1_10rem] rounded-field border border-rule bg-paper px-3 py-2.5 font-mono text-base break-all">
          {shown ? site.passcode : "•".repeat(Math.min(12, site.passcode.length))}
        </code>
        <Button variant="ghost" className="px-3" onClick={() => setShown(!shown)} aria-pressed={shown}>
          {shown ? "Hide" : "Show"}
        </Button>
        <CopyButton text={site.passcode} variant="ghost" className="px-3" />
      </div>
      <div className="mt-2">
        <CopyButton text={site.url + "\nPasscode: " + site.passcode} label="Copy link and passcode" variant="ghost" className="px-3" />
      </div>
      <form
        noValidate
        className="mt-5"
        onSubmit={async (e) => {
          e.preventDefault();
          if (!okCode(code)) return setError("Use a passcode of 6 to 128 characters.");
          setError("");
          if (await change({ passcode: code.trim() }, "Passcode changed. Everyone needs the new one now.")) setCode("");
        }}
      >
        <Field id="change-passcode" label="Change the passcode" error={error} hint="Everyone who used the old one must type the new one.">
          <div className="flex flex-wrap gap-2">
            <Input
              id="change-passcode"
              className="flex-[1_1_12rem]"
              value={code}
              onChange={(e) => (setCode(e.target.value), setError(""))}
              autoComplete="off"
              spellCheck={false}
              aria-invalid={!!error}
              aria-describedby="change-passcode-msg"
            />
            <Button type="submit">Change</Button>
          </div>
        </Field>
      </form>
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <Button onClick={() => change({ sign_everyone_out: true }, "Everyone must type the passcode again.")}>Sign everyone out</Button>
        <span className="text-sm text-ink-soft">The passcode stays the same.</span>
      </div>
    </>
  );
}

/** Named is the list in force: adding and removing happen at once (removing offers Undo). */
function Named({ site }: { site: Site }) {
  const change = useChange(site);
  const add = (e: string) => change({ viewers_add: [e] }, e + " can open " + site.name + ".");
  const remove = (e: string) => change({ viewers_remove: [e] }, e + " can no longer open " + site.name + ".", () => void add(e));
  return (
    <People
      list={site.viewers}
      onAdd={add}
      onRemove={(e) => void remove(e)}
    />
  );
}

/** Draft is the list to save with the switch to named people. */
function Draft({ list, onChange }: { list: string[]; onChange: (l: string[]) => void }) {
  return (
    <People
      list={list.map((email) => ({ email, signed_in: false, draft: true }))}
      onAdd={async (e) => (onChange([...list.filter((x) => x !== e), e]), true)}
      onRemove={(e) => onChange(list.filter((x) => x !== e))}
    />
  );
}

function People({
  list,
  onAdd,
  onRemove,
}: {
  list: { email: string; signed_in: boolean; draft?: boolean }[];
  onAdd: (email: string) => Promise<boolean>;
  onRemove: (email: string) => void;
}) {
  const [email, setEmail] = useState("");
  const [error, setError] = useState("");
  return (
    <>
      {list.length ? (
        <ul className="m-0 mb-3 list-none p-0" aria-label="People who can open it">
          {list.map((v) => (
            <Row
              key={v.email}
              end={
                <Button variant="ghost" className="px-3 text-bad" onClick={() => onRemove(v.email)} aria-label={"Remove " + v.email}>
                  Remove
                </Button>
              }
            >
              <span className="break-all">{v.email}</span>{" "}
              {!v.draft && (v.signed_in ? <Badge tone="ok">has signed in</Badge> : <Badge>not signed in yet</Badge>)}
            </Row>
          ))}
        </ul>
      ) : (
        <p className="mb-3 text-sm text-ink-soft">Nobody yet. Add the email addresses of the people you want in.</p>
      )}
      <form
        noValidate
        onSubmit={async (ev) => {
          ev.preventDefault();
          const e = email.trim().toLowerCase();
          if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(e)) return setError("That email address does not look right.");
          setError("");
          if (await onAdd(e)) setEmail("");
        }}
      >
        <label htmlFor="viewer-email" className="mb-1.5 block text-sm font-semibold">
          Add someone by email
        </label>
        <div className="flex flex-wrap gap-2">
          <Input
            id="viewer-email"
            type="email"
            className="flex-[1_1_14rem]"
            placeholder="name@example.com"
            value={email}
            onChange={(e) => (setEmail(e.target.value), setError(""))}
            aria-invalid={!!error}
            autoComplete="off"
          />
          <Button type="submit">Add</Button>
        </div>
        {error && <FieldMessage tone="bad">{error}</FieldMessage>}
      </form>
    </>
  );
}
