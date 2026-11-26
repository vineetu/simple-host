// The page behind a one-time upload link (/upload/<token>), opened from a chat,
// often on a phone, by someone who may not be signed in. Files mode: choose
// photos and files, name each, save them to the site's file store in one go.
// Archive mode: choose the whole site as one .zip or .tar.gz and publish it.
import { useEffect, useId, useRef, useState, type DragEvent, type ReactNode } from "react";
import { useParams } from "react-router";
import { ApiError } from "@/lib/api";
import { bytes, plural } from "@kit/lib/format";
import { cn } from "@kit/lib/utils";
import { Button } from "@kit/components/ui/button";
import { FieldMessage, Input, Label } from "@kit/components/ui/input";
import { PageShell } from "@kit/components/ui/page-shell";
import { Skeleton } from "@kit/components/ui/skeleton";
import {
  MAX_FILES,
  extOf,
  isArchive,
  isPhoto,
  sentShares,
  useUploadDeps,
  type FilesSaved,
  type Link,
  type Published,
} from "./data";

type Item = {
  id: number;
  original: string;
  before: number;
  photo: boolean;
  /** Making it smaller: the photo has no blob yet. */
  blob?: Blob;
  ext: string;
  name: string;
  thumb?: string;
};

type Say = { text: ReactNode; bad?: boolean } | null;

export function Upload() {
  const token = useParams().token || "";
  const deps = useUploadDeps();
  const [link, setLink] = useState<Link | null>(null);
  const [broken, setBroken] = useState("");

  useEffect(() => {
    if (!token) {
      setBroken("This link is not complete. Ask your chat for a new one.");
      return;
    }
    deps.check(token).then(setLink, (e: Error) => setBroken(e.message));
  }, [token, deps]);

  const archive = link?.kind === "archive";
  return (
    <PageShell title={archive ? "Publish the site" : "Add photos and files"}>
      <div className="mx-auto w-full max-w-prose pt-8 pb-4 md:pt-12">
        {broken ? (
          <LinkBroken why={broken} />
        ) : !link ? (
          <div role="status" aria-label="Checking the link">
            <Skeleton className="h-8 w-2/3" />
            <Skeleton className="mt-3 h-5 w-full" />
            <Skeleton className="mt-8 h-48 w-full" />
          </div>
        ) : archive ? (
          <ArchiveForm token={token} />
        ) : (
          <FilesForm token={token} namespace={link.namespace || "files"} />
        )}
        <p className="mt-10 text-sm text-ink-faint">
          <a href="/assets/photo-codecs/v3/README.txt">Photo decoder licences and source</a>
        </p>
      </div>
    </PageShell>
  );
}

function Title({ children, lead }: { children: ReactNode; lead: ReactNode }) {
  return (
    <>
      <h1 className="type-page md:text-3xl">{children}</h1>
      <p className="mt-2 text-ink-soft">{lead}</p>
    </>
  );
}

function LinkBroken({ why }: { why: string }) {
  return (
    <div className="rounded-wobble border-[1.5px] border-ink-soft bg-card px-6 py-7 shadow-paper md:px-9">
      <h1 className="type-card">This link does not work</h1>
      <FieldMessage tone="bad">{why}</FieldMessage>
      <p className="mt-4 text-ink-soft">
        Upload links work once and run out after a while. Ask in your chat for a new link, for example “give me a link to add photos”.
      </p>
    </div>
  );
}

/** Drop is the big target: the whole box is the file input's label, so a tap or a click opens the picker and files can be dropped on it. */
function Drop({
  id,
  accept,
  multiple,
  disabled,
  onFiles,
  title,
  hint,
  compact,
}: {
  id: string;
  accept?: string;
  multiple?: boolean;
  disabled?: boolean;
  onFiles: (f: File[]) => void;
  title: string;
  hint: string;
  compact?: boolean;
}) {
  const [over, setOver] = useState(false);
  const stop = (e: DragEvent) => {
    e.preventDefault();
    e.stopPropagation();
  };
  return (
    <label
      htmlFor={id}
      onDragEnter={(e) => (stop(e), setOver(true))}
      onDragOver={(e) => (stop(e), setOver(true))}
      onDragLeave={(e) => (stop(e), setOver(false))}
      onDrop={(e) => {
        stop(e);
        setOver(false);
        if (!disabled) onFiles(Array.from(e.dataTransfer.files));
      }}
      className={cn(
        "flex w-full cursor-pointer flex-col items-center justify-center gap-1 rounded-wobble border-2 border-dashed border-ink-soft bg-card px-6 text-center",
        "focus-within:outline-2 focus-within:outline-offset-2 focus-within:outline-accent hover:border-accent hover:bg-accent-soft/40",
        compact ? "min-h-24 py-4" : "min-h-52 py-8",
        over && "border-accent bg-accent-soft",
        disabled && "cursor-default opacity-55 hover:border-ink-soft hover:bg-card",
      )}
    >
      <input
        id={id}
        type="file"
        className="sr-only"
        accept={accept}
        multiple={multiple}
        disabled={disabled}
        onChange={(e) => {
          onFiles(Array.from(e.target.files || []));
          e.target.value = "";
        }}
      />
      {!compact && (
        <svg viewBox="0 0 48 48" aria-hidden="true" className="mb-2 size-12 text-accent" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round">
          <rect x="6" y="10" width="36" height="28" rx="4" />
          <circle cx="17" cy="20" r="3.5" />
          <path d="M6 33l10-9 8 7 6-5 12 10" />
          <path d="M38 4v12M32 10h12" />
        </svg>
      )}
      <span className="text-lg font-semibold text-ink">{title}</span>
      <span className="text-sm text-ink-soft">{hint}</span>
    </label>
  );
}

function Bar({ share, label }: { share: number; label: string }) {
  const p = Math.round(share * 100);
  return (
    <div role="progressbar" aria-label={label} aria-valuemin={0} aria-valuemax={100} aria-valuenow={p} className="h-2 overflow-hidden rounded-tag bg-paper-lo">
      <div className="h-full bg-accent transition-[width]" style={{ width: p + "%" }} />
    </div>
  );
}

const FileIcon = () => (
  <svg viewBox="0 0 24 24" aria-hidden="true" className="size-8 text-ink-soft" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinejoin="round">
    <path d="M6 3h8l4 4v14H6z" />
    <path d="M14 3v4h4" />
  </svg>
);

let nextId = 1;

function FilesForm({ token, namespace }: { token: string; namespace: string }) {
  const deps = useUploadDeps();
  const pickId = useId();
  const [items, setItems] = useState<Item[]>([]);
  const [left, setLeft] = useState<string[]>([]); // files that could not be added, with why
  const [say, setSay] = useState<Say>(null);
  const [sending, setSending] = useState<number[] | null>(null); // each file's share sent
  const [saved, setSaved] = useState<FilesSaved | null>(null);
  const thumbs = useRef<string[]>([]);
  useEffect(() => () => thumbs.current.forEach((u) => URL.revokeObjectURL(u)), []);

  const preparing = items.some((i) => !i.blob);
  const busy = preparing || sending !== null;

  const choose = async (files: File[]) => {
    if (busy || !files.length) return;
    setSay(null);
    if (files.length + items.length > MAX_FILES) {
      setLeft([]);
      setSay({ text: `Choose up to ${MAX_FILES} at a time. You picked ${files.length + items.length}; save these first, then ask your chat for another link.`, bad: true });
      return;
    }
    const why: string[] = [];
    const fresh: (Item & { file: File })[] = [];
    for (const f of files) {
      if (!isPhoto(f) && f.size > deps.filesLimit) {
        why.push(`${f.name} is ${bytes(f.size)}; a site's files can hold ${bytes(deps.filesLimit)} in all.`);
        continue;
      }
      fresh.push({ id: nextId++, file: f, original: f.name, before: f.size, photo: isPhoto(f), ext: isPhoto(f) ? "" : extOf(f.name), name: deps.slug(f.name) });
    }
    setItems((all) => [...all, ...fresh.map(({ file: _f, ...i }) => (isPhoto(_f) ? i : { ...i, blob: _f }))]);
    for (const it of fresh) {
      if (!it.photo) continue;
      try {
        const p = await deps.shrink(it.file);
        const thumb = URL.createObjectURL(p.blob);
        thumbs.current.push(thumb);
        setItems((all) => all.map((x) => (x.id === it.id ? { ...x, blob: p.blob, ext: p.ext, thumb } : x)));
      } catch (e) {
        why.push(`${it.original}: ${(e as Error).message}`);
        setItems((all) => all.filter((x) => x.id !== it.id));
      }
    }
    setLeft(why);
  };

  const remove = (id: number) => setItems((all) => all.filter((x) => x.id !== id));
  const rename = (id: number, name: string) => setItems((all) => all.map((x) => (x.id === id ? { ...x, name } : x)));

  const save = async () => {
    if (busy || !items.length) return;
    const named = items.map((i) => ({ ...i, name: deps.slug(i.name || i.original) }));
    setItems(named);
    const form = new FormData();
    for (const i of named) form.append("files", i.blob!, i.name + i.ext);
    const sizes = named.map((i) => i.blob!.size);
    setSay(null);
    setSending(sizes.map(() => 0));
    try {
      const b = await deps.send<FilesSaved>(token, form, null, (sent, total) => setSending(sentShares(sizes, sent, total)));
      setSaved({ namespace: b.namespace || namespace, files: b.files?.length ? b.files : named.map((i) => i.name + i.ext) });
    } catch (e) {
      setSending(null);
      setSay({ text: (e as ApiError).message, bad: true });
    }
  };

  if (saved) return <FilesDone saved={saved} thumbs={items} />;

  const total = items.reduce((a, i) => a + (i.blob?.size ?? 0), 0);
  const allSent = sending && sending.every((s) => s >= 1);
  return (
    <>
      <Title lead={<>Choose them from your phone or computer. They are saved to the site’s “{namespace}” files. Photos are made smaller on this device first, and camera and location details are removed.</>}>
        Add photos and files
      </Title>
      <div className="mt-6">
        <Drop
          id={pickId}
          multiple
          disabled={busy}
          onFiles={choose}
          compact={items.length > 0}
          title={items.length ? "Add more" : "Choose photos or files"}
          hint={`Tap to choose, or drop them here. Up to ${MAX_FILES} at a time.`}
        />
        {say && !items.length && <FieldMessage tone={say.bad ? "bad" : undefined}>{say.text}</FieldMessage>}
        {left.length > 0 && (
          <FieldMessage tone="bad">
            Left out: {left.join(" ")}
          </FieldMessage>
        )}
      </div>

      {items.length > 0 && (
        <>
          <p id="name-hint" className="mt-6 text-sm text-ink-soft">
            Give each one a short name, such as kitchen or menu. It becomes part of the file’s address.
          </p>
          <ul className="mt-2 border-t border-rule" aria-label="Chosen files">
            {items.map((it, n) => (
              <FileRow
                key={it.id}
                it={it}
                share={sending?.[n]}
                done={!!allSent}
                locked={sending !== null}
                onRename={(v) => rename(it.id, v)}
                onRemove={() => remove(it.id)}
              />
            ))}
          </ul>
          <div className="sticky bottom-0 -mx-4 mt-4 border-t border-rule bg-paper/95 px-4 pt-3 pb-[max(12px,env(safe-area-inset-bottom))] backdrop-blur-sm md:static md:mx-0 md:border-0 md:bg-transparent md:px-0 md:backdrop-blur-none">
            {sending ? (
              <div role="status">
                <p className="mb-2 text-sm font-medium">
                  {allSent ? "Saving on the site…" : `Sending ${plural(items.length, "file")}… ${Math.round((sending.reduce((a, s, i) => a + s * (items[i].blob?.size ?? 0), 0) / (total || 1)) * 100)}%`}
                </p>
                <Bar share={sending.reduce((a, s, i) => a + s * (items[i].blob?.size ?? 0), 0) / (total || 1)} label="Sent so far" />
              </div>
            ) : (
              <div className="flex flex-col gap-3 md:flex-row md:items-center">
                <p className="text-sm text-ink-soft md:mr-auto" role="status">
                  {preparing ? "Making photos smaller…" : `${plural(items.length, "file")} ready · ${bytes(total)} in all`}
                </p>
                <Button variant="primary" size="lg" className="md:w-auto md:px-8" disabled={busy} onClick={save}>
                  Save to site
                </Button>
              </div>
            )}
            {say && items.length > 0 && <FieldMessage tone={say.bad ? "bad" : undefined}>{say.text}</FieldMessage>}
          </div>
        </>
      )}
    </>
  );
}

function FileRow({
  it,
  share,
  done,
  locked,
  onRename,
  onRemove,
}: {
  it: Item;
  share?: number;
  done: boolean;
  locked: boolean;
  onRename: (v: string) => void;
  onRemove: () => void;
}) {
  const id = useId();
  const state = !it.blob ? "Making it smaller…" : share === undefined ? null : share >= 1 ? (done ? "Sent, saving…" : "Sent") : share > 0 ? "Sending…" : "Waiting";
  return (
    <li className="grid grid-cols-[64px_minmax(0,1fr)] gap-x-3 gap-y-2 border-b border-rule py-3 md:grid-cols-[80px_minmax(0,1fr)_auto] md:gap-x-4">
      <div className="flex size-16 items-center justify-center overflow-hidden rounded-field bg-paper-lo md:size-20">
        {it.thumb ? <img src={it.thumb} alt="" className="size-full object-cover" /> : !it.blob ? <Skeleton className="size-full" /> : <FileIcon />}
      </div>
      <div className="min-w-0">
        <Label htmlFor={id} className="mb-1">
          Name
        </Label>
        <div className="flex items-center gap-1.5">
          <Input
            id={id}
            value={it.name}
            maxLength={60}
            disabled={locked}
            aria-describedby="name-hint"
            autoComplete="off"
            autoCapitalize="none"
            spellCheck={false}
            onChange={(e) => onRename(e.target.value)}
            className="h-11"
          />
          <span className="w-11 flex-none text-sm text-ink-soft">{it.ext}</span>
        </div>
        <p className="mt-1 text-sm text-ink-soft">
          {it.original} · {bytes(it.before)}
          {it.photo && it.blob && <> → {bytes(it.blob.size)}</>}
        </p>
        {state && (
          <div className="mt-1.5 flex items-center gap-2">
            {share !== undefined && <div className="w-24 flex-none"><Bar share={share} label={"Sent of " + it.original} /></div>}
            <span className={cn("text-sm", share !== undefined && share >= 1 ? "text-good" : "text-ink-soft")}>{state}</span>
          </div>
        )}
      </div>
      {!locked && (
        <div className="col-start-2 md:col-start-3 md:row-start-1">
          <Button onClick={onRemove} aria-label={"Remove " + it.original} disabled={!it.blob}>
            Remove
          </Button>
        </div>
      )}
    </li>
  );
}

function Copyable({ text }: { text: string }) {
  const [said, setSaid] = useState("");
  return (
    <>
      <p className="mt-2 rounded-field border-[1.5px] border-dashed border-ink-soft bg-card px-4 py-3 select-all">{text}</p>
      <div className="mt-3 flex flex-wrap items-center gap-3">
        <Button
          variant="primary"
          size="lg"
          className="md:w-auto md:px-8"
          onClick={() =>
            navigator.clipboard.writeText(text).then(
              () => setSaid("Copied. Paste it into your chat."),
              () => setSaid("Could not copy here. Select the message above and copy it."),
            )
          }
        >
          Copy message
        </Button>
        <span role="status" className="text-sm text-good">
          {said}
        </span>
      </div>
    </>
  );
}

function Done({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="rounded-wobble border-[1.5px] border-ink-soft bg-card px-6 py-7 shadow-paper md:px-9">
      <div className="flex items-center gap-3">
        <span aria-hidden="true" className="flex size-10 flex-none items-center justify-center rounded-tag bg-good text-xl text-on-accent">
          ✓
        </span>
        <h1 className="type-card">{title}</h1>
      </div>
      {children}
      <p className="mt-6 text-sm text-ink-soft">This link has now been used. To add more, ask your chat for a new one.</p>
    </div>
  );
}

function FilesDone({ saved, thumbs }: { saved: FilesSaved; thumbs: Item[] }) {
  const message = `I added ${saved.files.join(", ")} to the site's ${saved.namespace} files.`;
  return (
    <Done title="Saved to the site">
      <p className="mt-3 text-ink-soft">
        {plural(saved.files.length, "file")} in the site’s “{saved.namespace}” files:
      </p>
      <ul className="mt-2 border-t border-rule" aria-label="Saved files">
        {saved.files.map((f, i) => (
          <li key={f} className="flex items-center gap-3 border-b border-rule py-2">
            <span className="flex size-10 flex-none items-center justify-center overflow-hidden rounded-sm bg-paper-lo">
              {thumbs[i]?.thumb ? <img src={thumbs[i].thumb} alt="" className="size-full object-cover" /> : <FileIcon />}
            </span>
            <span className="min-w-0 font-mono text-sm [overflow-wrap:anywhere]">{f}</span>
          </li>
        ))}
      </ul>
      <h2 className="mt-6 type-section">Tell your chat</h2>
      <p className="mt-1 text-sm text-ink-soft">Paste this into your chat so it can use them:</p>
      <Copyable text={message} />
    </Done>
  );
}

function ArchiveForm({ token }: { token: string }) {
  const deps = useUploadDeps();
  const pickId = useId();
  const [file, setFile] = useState<File | null>(null);
  const [say, setSay] = useState<Say>(null);
  const [share, setShare] = useState<number | null>(null);
  const [done, setDone] = useState<Published | null>(null);

  const pick = (files: File[]) => {
    setSay(null);
    const f = files[0];
    if (!f) return;
    if (files.length > 1) return setSay({ text: "Choose one file: the whole site as a single .zip or .tar.gz.", bad: true });
    if (!isArchive(f)) return setSay({ text: `${f.name} is not a .zip or .tar.gz file. Put the site's files in one archive first.`, bad: true });
    if (f.size > deps.siteLimit) return setSay({ text: `${f.name} is ${bytes(f.size)}; a site can hold ${bytes(deps.siteLimit)} of files.`, bad: true });
    setFile(f);
  };
  const publish = async () => {
    if (!file || share !== null) return;
    setSay(null);
    setShare(0);
    try {
      const b = await deps.send<Published>(token, file, file.type || "application/octet-stream", (s, t) => setShare(s / (t || 1)));
      setDone(b);
    } catch (e) {
      setShare(null);
      setSay({ text: (e as ApiError).message, bad: true });
    }
  };

  if (done)
    return (
      <Done title={done.version ? `Version ${done.version} is live` : "Published"}>
        <p className="mt-3 text-ink-soft">Every file in {file?.name} is live at once, as the site’s next version.</p>
        {done.url && (
          <>
            <p className="mt-2 font-mono text-sm [overflow-wrap:anywhere]">{done.url}</p>
            <Button asChild variant="primary" size="lg" className="mt-4 md:w-auto md:px-8">
              <a href={done.url} target="_blank" rel="noopener">
                Open the site
              </a>
            </Button>
          </>
        )}
      </Done>
    );

  return (
    <>
      <Title lead="Choose the whole site as one .zip or .tar.gz file. Every file in it goes live at once, as the site’s next version; the one before stays in its versions.">
        Publish the site
      </Title>
      <div className="mt-6">
        <Drop
          id={pickId}
          accept=".zip,.tar.gz,.tgz,application/zip,application/gzip"
          disabled={share !== null}
          onFiles={pick}
          compact={!!file}
          title={file ? "Choose another file" : "Choose the site’s archive"}
          hint="Tap to choose, or drop it here. One .zip or .tar.gz file."
        />
      </div>
      {file && (
        <div className="mt-4 flex items-center gap-3 border-y border-rule py-3">
          <FileIcon />
          <div className="min-w-0 flex-1">
            <p className="font-medium [overflow-wrap:anywhere]">{file.name}</p>
            <p className="text-sm text-ink-soft">{bytes(file.size)}</p>
          </div>
        </div>
      )}
      <div className="mt-4">
        {share !== null ? (
          <div role="status">
            <p className="mb-2 text-sm font-medium">{share >= 1 ? "Publishing on the site…" : `Sending… ${Math.round(share * 100)}%`}</p>
            <Bar share={share} label="Sent so far" />
          </div>
        ) : (
          <Button variant="primary" size="lg" className="md:w-auto md:px-8" disabled={!file} onClick={publish}>
            Publish
          </Button>
        )}
        {say && <FieldMessage tone={say.bad ? "bad" : undefined}>{say.text}</FieldMessage>}
      </div>
    </>
  );
}
