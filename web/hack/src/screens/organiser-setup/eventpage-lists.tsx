// Schedule, Sponsors and FAQ: the three lists in the content document. Each
// dialog's Save, each move and each Remove writes at once; Remove offers
// Undo instead of asking. All three are read-only once the event has ended
// (the server refuses PUT /content then) and under an admin inspection.
import { useRef, useState, type DragEvent, type FormEvent, type KeyboardEvent, type ReactNode } from "react";
import { Button } from "@kit/components/ui/button";
import { Dialog, DialogActions } from "@kit/components/ui/dialog";
import { EmptyState } from "@kit/components/ui/empty-state";
import { Field, FieldMessage, Input, Label, NativeSelect, Textarea } from "@kit/components/ui/input";
import { Menu, type MenuItem } from "@kit/components/ui/menu";
import { Skeleton } from "@kit/components/ui/skeleton";
import { useToast } from "@kit/components/ui/toast";
import { cn } from "@kit/lib/utils";
import { AppLink } from "@/app/nav";
import { DateTimePair, fileAsDataURL, localInput } from "./common";
import {
  checkImage,
  checkTime,
  groupByDay,
  label,
  LOGO_TYPES,
  merge,
  move,
  moveTo,
  MOVES,
  setInLink,
  sponsorLine,
  when,
  type Content,
  type Faq,
  type Row,
  type ScheduleItem,
  type Sponsor,
} from "./eventpage-data";
import { ContentFailed, current, CustomSiteNote, EndedReadOnly, FilePick, Pill, PlusIcon, TabHead, usePersist, type TabProps } from "./eventpage-ui";

const listBox = "mt-4 max-w-prose rounded-field border-[1.5px] border-rule bg-card px-3 py-1";
const rowBox = "flex min-h-14 items-center gap-3 border-b border-rule px-1 py-2 last:border-b-0";

function ListLoading() {
  return (
    <div className="mt-4 max-w-prose" role="status" aria-busy="true" aria-label="Loading">
      <Skeleton className="h-14 w-full" />
      <Skeleton className="mt-2 h-14 w-full" />
    </div>
  );
}

/** The state a list tab is in before its rows: loading, failed, or the notes above them. */
function useListFrame(p: TabProps, what: string) {
  const readOnly = p.admin || p.ended;
  const notes = (
    <>
      <CustomSiteNote website={p.website.data} readOnly={readOnly} />
      {p.ended && !p.admin && <EndedReadOnly what={what} />}
    </>
  );
  const blocked = p.content.isPending ? <ListLoading /> : p.content.isError ? <ContentFailed content={p.content} /> : null;
  return { readOnly, notes, blocked };
}

/** RowActions is a row's Edit and its menu. */
function RowActions({ name, items, onEdit }: { name: string; items: MenuItem[]; onEdit: () => void }) {
  return (
    <div className="flex flex-none items-center gap-2">
      <Button className="hidden md:inline-flex" onClick={onEdit} aria-label={"Edit " + name}>
        Edit
      </Button>
      <Menu label={"More for " + name} items={items} />
    </div>
  );
}

/**
 * Reorderable is a list whose rows move by dragging (wide screens), by the
 * grip's arrow keys, or by the row menu's Move up and Move down.
 */
function useReorder<T>(items: T[], save: (next: T[], moved: { name: string; to: number }) => Promise<void>, nameOf: (t: T) => string) {
  const from = useRef<number | null>(null);
  const busy = useRef(false);
  const [status, setStatus] = useState("");
  const run = async (next: T[], name: string, to: number) => {
    if (busy.current || next === items) return;
    busy.current = true;
    try {
      await save(next, { name, to });
      setStatus(`${name} moved to position ${to + 1} of ${next.length}`);
    } finally {
      busy.current = false;
    }
  };
  const step = (i: number, dir: number) => run(move(items, i, dir), nameOf(items[i]), i + dir);
  const rowProps = (i: number) => ({
    draggable: true,
    onDragStart: (e: DragEvent) => {
      from.current = i;
      e.dataTransfer.effectAllowed = "move";
    },
    onDragEnd: () => (from.current = null),
    onDragOver: (e: DragEvent) => e.preventDefault(),
    onDrop: (e: DragEvent) => {
      e.preventDefault();
      const f = from.current;
      from.current = null;
      if (e.dataTransfer.files?.length || f == null || f === i) return;
      void run(moveTo(items, f, i), nameOf(items[f]), i);
    },
  });
  const grip = (i: number) => (
    <button
      type="button"
      className="hidden size-11 flex-none cursor-grab place-items-center rounded-field text-ink-soft hover:text-ink md:grid"
      aria-roledescription="drag handle"
      aria-label={`Move ${nameOf(items[i])}, position ${i + 1} of ${items.length}. Use the up and down arrow keys.`}
      onKeyDown={(e: KeyboardEvent) => {
        if (e.key !== "ArrowUp" && e.key !== "ArrowDown") return;
        e.preventDefault();
        void step(i, e.key === "ArrowUp" ? -1 : 1);
      }}
    >
      <svg viewBox="0 0 12 16" className="h-4 w-3" aria-hidden="true" fill="currentColor">
        {[3, 8, 13].flatMap((y) => [<circle key={"a" + y} cx="3" cy={y} r="1.4" />, <circle key={"b" + y} cx="9" cy={y} r="1.4" />])}
      </svg>
    </button>
  );
  const moveItems = (i: number): MenuItem[] => [
    i === 0 ? { label: "Move up", disabled: true } : { label: "Move up", onSelect: () => void step(i, -1) },
    i === items.length - 1 ? { label: "Move down", disabled: true } : { label: "Move down", onSelect: () => void step(i, 1) },
  ];
  const live = (
    <p className="sr-only" role="status" aria-live="polite">
      {status}
    </p>
  );
  return { rowProps, grip, moveItems, live };
}

/** useRemove takes a row out at once and offers Undo (which writes the list back as it was). */
function useRemove(p: TabProps) {
  const persist = usePersist(p.slug);
  const toast = useToast();
  return async (next: Content, text: string) => {
    const before = current(p.content);
    if ((await persist(next)) !== true) return;
    toast(text, {
      undo: {
        label: "Undo",
        run: () => void persist(before),
      },
    });
  };
}

// ------------------------------------------------------------------ Schedule

function TimeDialog({ p, existing, onClose }: { p: TabProps; existing: ScheduleItem | null; onClose: () => void }) {
  const zone = p.ev.time_zone;
  const persist = usePersist(p.slug);
  const [sd0, st0] = existing ? localInput(existing.start_at, zone).split("T") : ["", ""];
  const [ed0, et0] = existing?.end_at ? localInput(existing.end_at, zone).split("T") : ["", ""];
  const [title, setTitle] = useState(existing?.title || "");
  const [desc, setDesc] = useState(existing?.description || "");
  const [sd, setSd] = useState(sd0 || "");
  const [st, setSt] = useState(st0 || "");
  const [ed, setEd] = useState(ed0 || "");
  const [et, setEt] = useState(et0 || "");
  const [prompt, setPrompt] = useState(existing?.prompt || "");
  const [errs, setErrs] = useState<{ title?: string; end?: string; form?: string }>({});
  const [busy, setBusy] = useState(false);
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const found = checkTime(title, sd, st, ed, et);
    setErrs(found);
    if (found.title || found.end) return;
    const next: ScheduleItem = { title: title.trim(), description: desc, start_at: sd + "T" + st, end_at: ed + "T" + et };
    if (prompt) next.prompt = prompt;
    const doc = current(p.content);
    const schedule = doc.schedule.slice();
    if (!existing) schedule.push(next);
    else {
      const at = schedule.indexOf(existing);
      if (at === -1) return setErrs({ form: "This item changed elsewhere. Reload and try again." });
      schedule[at] = next;
    }
    setBusy(true);
    const r = await persist({ ...doc, schedule }, { quiet: true });
    setBusy(false);
    if (r === true) onClose();
    else setErrs({ form: String(r) });
  };
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()} title={existing ? "Edit time" : "Add time"} description={`Times are in ${zone}.`}>
      <form onSubmit={submit} noValidate>
        <Field id="d-title" label="Title" error={errs.title}>
          <Input id="d-title" maxLength={120} value={title} onChange={(e) => setTitle(e.target.value)} aria-invalid={!!errs.title} autoFocus />
        </Field>
        <Field id="d-desc" label="Details (optional)">
          <Textarea id="d-desc" rows={2} maxLength={500} value={desc} onChange={(e) => setDesc(e.target.value)} className="min-h-20" />
        </Field>
        <DateTimePair id="d-start" label="Starts" date={sd} time={st} onDate={setSd} onTime={setSt} />
        <DateTimePair id="d-end" label="Ends" date={ed} time={et} onDate={setEd} onTime={setEt} error={errs.end} />
        <Field id="d-prompt" label="When this starts, ask to (optional)" hint="Home asks you at this time, like “Kickoff is now. Open sign-up?” Nothing changes until you press the button.">
          <NativeSelect id="d-prompt" className="w-full" value={prompt} onChange={(e) => setPrompt(e.target.value)} aria-describedby="d-prompt-msg">
            <option value="">Nothing</option>
            {Object.entries(MOVES).map(([v, l]) => (
              <option key={v} value={v}>
                {l}
              </option>
            ))}
          </NativeSelect>
        </Field>
        {errs.form && <FieldMessage tone="bad">{errs.form}</FieldMessage>}
        <DialogActions>
          <Button onClick={onClose}>Cancel</Button>
          <Button type="submit" variant="primary" disabled={busy}>
            {existing ? "Save" : "Add time"}
          </Button>
        </DialogActions>
      </form>
    </Dialog>
  );
}

const LockIcon = () => (
  <svg viewBox="0 0 20 20" className="size-3.5" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    <rect x="5" y="9" width="10" height="8" rx="1.5" />
    <path d="M7 9V6.5a3 3 0 0 1 6 0V9" />
  </svg>
);

function ScheduleRow({ r, p, readOnly, onEdit, onRemove }: { r: Row; p: TabProps; readOnly: boolean; onEdit: () => void; onRemove: () => void }) {
  const [start, end] = when(r, p.ev.time_zone);
  const where = r.fixed ? setInLink(r.set_in, p.slug, r.key || "") : null;
  const prompt = r.prompt && MOVES[r.prompt] ? <span className="block text-sm text-ink-faint">Then asks: {MOVES[r.prompt]}?</span> : null;
  const pills = (
    <>
      {r.now ? <Pill tone="accent">Now</Pill> : r.next ? <Pill>Next</Pill> : null}
      {r.fixed && (
        <span title="Comes from the event's settings">
          <Pill>
            <LockIcon />
            Fixed
          </Pill>
        </span>
      )}
    </>
  );
  const text = (
    <>
      <span className="block font-semibold text-ink">{r.title}</span>
      {r.fixed ? r.description && <span className="block text-sm text-ink-soft">{r.description}</span> : <span className={cn("block text-sm", r.description ? "text-ink-soft" : "text-ink-faint")}>{r.description || "No details"}</span>}
      {prompt}
    </>
  );
  return (
    <li className={rowBox}>
      <span className="w-24 flex-none self-start pt-0.5 text-sm md:w-32">
        <span className="block font-semibold text-ink">{start}</span>
        {end && <span className="block text-ink-soft">{end}</span>}
      </span>
      <span className="min-w-0 flex-1">
        {readOnly || r.fixed ? (
          <span className="block">{text}</span>
        ) : (
          <button type="button" onClick={onEdit} className="block min-h-11 w-full cursor-pointer text-left">
            {text}
          </button>
        )}
        {where && (
          <span className="block text-sm text-ink-soft">
            Set in <AppLink to={where.href}>{where.label}</AppLink>
          </span>
        )}
        {(r.now || r.next || r.fixed) && <span className="mt-1 flex flex-wrap gap-1.5 md:hidden">{pills}</span>}
      </span>
      <span className="flex flex-none flex-wrap items-center justify-end gap-2">
        <span className="hidden items-center gap-2 md:flex">{pills}</span>
        {!readOnly && !r.fixed && <RowActions name={r.title} onEdit={onEdit} items={[{ label: "Edit", onSelect: onEdit }, { separator: true }, { label: "Remove", onSelect: onRemove, danger: true }]} />}
      </span>
    </li>
  );
}

export function Schedule(p: TabProps) {
  const { readOnly, notes, blocked } = useListFrame(p, "Schedule");
  const remove = useRemove(p);
  const [open, setOpen] = useState<{ item: ScheduleItem | null } | null>(null);
  const zone = p.ev.time_zone;
  const loaded = !blocked;
  const add = (
    <Button variant="primary" onClick={() => setOpen({ item: null })}>
      <PlusIcon />
      Add time
    </Button>
  );
  const doc = current(p.content);
  const fixed = p.content.data?.fixed_schedule || [];
  const rows = label(merge(doc.schedule, fixed, zone));
  const head = <TabHead title="Schedule" sub={`In ${zone}. Your page lists them in time order and marks Now and Next.`} action={!readOnly && loaded && rows.length ? add : undefined} />;
  if (blocked)
    return (
      <>
        {head}
        {notes}
        {blocked}
      </>
    );
  return (
    <>
      {head}
      {notes}
      {rows.length ? (
        <>
          {groupByDay(rows, zone).map((g) => (
            <section key={g.day} aria-label={g.day} className="max-w-prose">
              <h3 className="mt-5 type-label text-ink-soft">{g.day}</h3>
              <ul className={cn(listBox, "mt-1.5 list-none")}>
                {g.rows.map((r, i) => (
                  <ScheduleRow
                    key={(r.fixed ? "f-" + r.key : "t-") + i + r.start_at}
                    r={r}
                    p={p}
                    readOnly={readOnly}
                    onEdit={() => r.src && setOpen({ item: r.src })}
                    onRemove={() => r.src && remove({ ...doc, schedule: doc.schedule.filter((x) => x !== r.src) }, `Removed “${r.title}”`)}
                  />
                ))}
              </ul>
            </section>
          ))}
          {fixed.length > 0 && <p className="mt-3 max-w-prose text-sm text-ink-soft">Rows marked Fixed come from your event settings, so they always match. Change them where they are set.</p>}
        </>
      ) : readOnly ? (
        <p className="mt-4 text-ink-soft">Nothing scheduled.</p>
      ) : (
        <EmptyState title="Nothing scheduled yet" action={add}>
          Add the kickoff, build time and demos. They show on your page, and participants see what is on now. Your dates and entry deadline show here on their own once you set them.
        </EmptyState>
      )}
      {open && <TimeDialog p={p} existing={open.item} onClose={() => setOpen(null)} />}
    </>
  );
}

// ------------------------------------------------------------------ Sponsors

const PictureIcon = () => (
  <svg viewBox="0 0 20 20" className="size-5" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    <path d="M3 4h14v12H3zM3 13l4-4 3 3 2-2 5 5M13 8h.01" />
  </svg>
);

function Thumb({ src, size = "size-10" }: { src?: string; size?: string }) {
  return src ? (
    <img src={src} alt="" className={cn(size, "flex-none rounded-sm border border-rule bg-white object-contain")} />
  ) : (
    <span role="img" aria-label="No logo" className={cn(size, "grid flex-none place-items-center rounded-sm border border-dashed border-ink-soft text-ink-soft")}>
      <PictureIcon />
    </span>
  );
}

function SponsorDialog({ p, existing, onClose }: { p: TabProps; existing: Sponsor | null; onClose: () => void }) {
  const persist = usePersist(p.slug);
  const [name, setName] = useState(existing?.name || "");
  const [tier, setTier] = useState(existing?.tier || "");
  const [url, setUrl] = useState(existing?.url || "");
  const [logo, setLogo] = useState(existing?.logo_data || "");
  const [logoName, setLogoName] = useState(existing?.logo_data ? "Current logo" : "No logo");
  const [errs, setErrs] = useState<{ name?: string; url?: string; logo?: string; form?: string }>({});
  const [busy, setBusy] = useState(false);
  const pick = async (file: File) => {
    const bad = checkImage(file);
    if (bad) return setErrs((e) => ({ ...e, logo: bad }));
    try {
      setLogo(await fileAsDataURL(file));
      setLogoName(file.name + " · not saved yet");
      setErrs((e) => ({ ...e, logo: undefined }));
    } catch {
      setErrs((e) => ({ ...e, logo: "Could not read that file." }));
    }
  };
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const found: typeof errs = {};
    if (!name.trim()) found.name = "Name is required.";
    if (url.trim() && !/^https:\/\//i.test(url.trim())) found.url = "Use a full address that starts with https://";
    setErrs(found);
    if (found.name || found.url) return;
    const next: Sponsor = { name: name.trim(), tier, url: url.trim(), logo_data: logo };
    const doc = current(p.content);
    const sponsors = doc.sponsors.slice();
    if (!existing) sponsors.push(next);
    else {
      const at = sponsors.indexOf(existing);
      if (at === -1) return setErrs({ form: "This item changed elsewhere. Reload and try again." });
      sponsors[at] = next;
    }
    setBusy(true);
    const r = await persist({ ...doc, sponsors }, { quiet: true });
    setBusy(false);
    if (r === true) onClose();
    else setErrs({ form: String(r) });
  };
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()} title={existing ? "Edit sponsor" : "Add sponsor"}>
      <form onSubmit={submit} noValidate>
        <div className="mb-4" role="group" aria-labelledby="d-logo-l">
          <Label id="d-logo-l">Logo</Label>
          <div className="flex flex-wrap items-center gap-3">
            <Thumb src={logo} size="size-14" />
            <span className="min-w-0 flex-1 text-sm text-ink-soft">{logoName}</span>
            <FilePick label={logo ? "Replace" : "Choose logo"} accept={LOGO_TYPES.join(",")} onFile={pick} />
            {logo && (
              <Button onClick={() => (setLogo(""), setLogoName("No logo"))} aria-label="Remove logo">
                Remove
              </Button>
            )}
          </div>
          {errs.logo ? <FieldMessage tone="bad">{errs.logo}</FieldMessage> : <p className="mt-1.5 text-sm text-ink-soft">PNG, JPEG or WebP, up to 128 KB. Square logos look best.</p>}
        </div>
        <Field id="d-name" label="Name" error={errs.name}>
          <Input id="d-name" maxLength={100} value={name} onChange={(e) => setName(e.target.value)} aria-invalid={!!errs.name} autoFocus />
        </Field>
        <Field id="d-tier" label="Tier (optional)" hint="For example Gold, Silver or Community partner.">
          <Input id="d-tier" maxLength={60} value={tier} onChange={(e) => setTier(e.target.value)} aria-describedby="d-tier-msg" />
        </Field>
        <Field id="d-link" label="Link (optional)" error={errs.url}>
          <Input id="d-link" type="url" inputMode="url" value={url} onChange={(e) => setUrl(e.target.value)} aria-invalid={!!errs.url} placeholder="https://" />
        </Field>
        {errs.form && <FieldMessage tone="bad">{errs.form}</FieldMessage>}
        <DialogActions>
          <Button onClick={onClose}>Cancel</Button>
          <Button type="submit" variant="primary" disabled={busy}>
            {existing ? "Save sponsor" : "Add sponsor"}
          </Button>
        </DialogActions>
      </form>
    </Dialog>
  );
}

/** OrderedList is Sponsors' and FAQ's shared list: rows in public-page order, each with its grip, Edit and menu. */
function OrderedList<T>({
  label: name,
  items,
  readOnly,
  nameOf,
  render,
  onEdit,
  onRemove,
  onReorder,
  foot,
}: {
  label: string;
  items: T[];
  readOnly: boolean;
  nameOf: (t: T) => string;
  render: (t: T) => ReactNode;
  onEdit: (t: T) => void;
  onRemove: (t: T) => void;
  onReorder: (next: T[]) => Promise<void>;
  foot: string;
}) {
  const r = useReorder(items, (next) => onReorder(next), nameOf);
  return (
    <>
      <ul aria-label={name} className={cn(listBox, "list-none")}>
        {items.map((t, i) => (
          <li key={i + nameOf(t)} className={rowBox} {...(readOnly ? {} : r.rowProps(i))}>
            {!readOnly && r.grip(i)}
            {readOnly ? (
              <span className="flex min-w-0 flex-1 items-center gap-3">{render(t)}</span>
            ) : (
              <button type="button" className="flex min-h-11 min-w-0 flex-1 cursor-pointer items-center gap-3 text-left" onClick={() => onEdit(t)}>
                {render(t)}
              </button>
            )}
            {!readOnly && (
              <RowActions name={nameOf(t)} onEdit={() => onEdit(t)} items={[{ label: "Edit", onSelect: () => onEdit(t) }, ...r.moveItems(i), { separator: true }, { label: "Remove", onSelect: () => onRemove(t), danger: true }]} />
            )}
          </li>
        ))}
      </ul>
      <p className="mt-2 max-w-prose text-sm text-ink-soft">{foot}</p>
      {!readOnly && r.live}
    </>
  );
}

export function Sponsors(p: TabProps) {
  const { readOnly, notes, blocked } = useListFrame(p, "Sponsors");
  const remove = useRemove(p);
  const persist = usePersist(p.slug);
  const [open, setOpen] = useState<{ item: Sponsor | null } | null>(null);
  const add = (
    <Button variant="primary" onClick={() => setOpen({ item: null })}>
      <PlusIcon />
      Add sponsor
    </Button>
  );
  const doc = current(p.content);
  const head = <TabHead title="Sponsors" sub="Shown on your page in this order. Drag a row, or use its menu, to move it." action={!readOnly && !blocked && doc.sponsors.length ? add : undefined} />;
  if (blocked)
    return (
      <>
        {head}
        {notes}
        {blocked}
      </>
    );
  return (
    <>
      {head}
      {notes}
      {doc.sponsors.length ? (
        <OrderedList
          label="Sponsors"
          items={doc.sponsors}
          readOnly={readOnly}
          nameOf={(s) => s.name}
          render={(s) => (
            <>
              <Thumb src={s.logo_data} />
              <span className="min-w-0">
                <span className="block font-semibold text-ink">{s.name}</span>
                <span className="block text-sm text-ink-soft">{sponsorLine(s)}</span>
              </span>
            </>
          )}
          onEdit={(s) => setOpen({ item: s })}
          onRemove={(s) => remove({ ...doc, sponsors: doc.sponsors.filter((x) => x !== s) }, `Removed ${s.name}`)}
          onReorder={async (sponsors) => void (await persist({ ...doc, sponsors }))}
          foot={`${doc.sponsors.length} of 20 sponsors · Logos: PNG, JPEG or WebP, up to 128 KB`}
        />
      ) : readOnly ? (
        <p className="mt-4 text-ink-soft">No sponsors.</p>
      ) : (
        <EmptyState title="No sponsors yet" action={add}>
          Logos and names show near the bottom of your page.
        </EmptyState>
      )}
      {open && <SponsorDialog p={p} existing={open.item} onClose={() => setOpen(null)} />}
    </>
  );
}

// ----------------------------------------------------------------------- FAQ

function FaqDialog({ p, existing, onClose }: { p: TabProps; existing: Faq | null; onClose: () => void }) {
  const persist = usePersist(p.slug);
  const [question, setQ] = useState(existing?.question || "");
  const [answer, setA] = useState(existing?.answer || "");
  const [errs, setErrs] = useState<{ q?: string; a?: string; form?: string }>({});
  const [busy, setBusy] = useState(false);
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const found: typeof errs = {};
    if (!question.trim()) found.q = "Question is required.";
    if (!answer.trim()) found.a = "Answer is required.";
    setErrs(found);
    if (found.q || found.a) return;
    const next = { question: question.trim(), answer: answer.trim() };
    const doc = current(p.content);
    const faq = doc.faq.slice();
    if (!existing) faq.push(next);
    else {
      const at = faq.indexOf(existing);
      if (at === -1) return setErrs({ form: "This item changed elsewhere. Reload and try again." });
      faq[at] = next;
    }
    setBusy(true);
    const r = await persist({ ...doc, faq }, { quiet: true });
    setBusy(false);
    if (r === true) onClose();
    else setErrs({ form: String(r) });
  };
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()} title={existing ? "Edit question" : "Add question"}>
      <form onSubmit={submit} noValidate>
        <Field id="d-q" label="Question" error={errs.q}>
          <Input id="d-q" maxLength={200} value={question} onChange={(e) => setQ(e.target.value)} aria-invalid={!!errs.q} autoFocus />
        </Field>
        <Field id="d-a" label="Answer" error={errs.a}>
          <Textarea id="d-a" rows={4} maxLength={2000} value={answer} onChange={(e) => setA(e.target.value)} aria-invalid={!!errs.a} />
        </Field>
        {errs.form && <FieldMessage tone="bad">{errs.form}</FieldMessage>}
        <DialogActions>
          <Button onClick={onClose}>Cancel</Button>
          <Button type="submit" variant="primary" disabled={busy}>
            {existing ? "Save" : "Add question"}
          </Button>
        </DialogActions>
      </form>
    </Dialog>
  );
}

export function FaqTab(p: TabProps) {
  const { readOnly, notes, blocked } = useListFrame(p, "FAQ");
  const remove = useRemove(p);
  const persist = usePersist(p.slug);
  const [open, setOpen] = useState<{ item: Faq | null } | null>(null);
  const add = (
    <Button variant="primary" onClick={() => setOpen({ item: null })}>
      <PlusIcon />
      Add question
    </Button>
  );
  const doc = current(p.content);
  const head = <TabHead title="FAQ" sub="Questions and answers, in this order. Answers start folded on your page." action={!readOnly && !blocked && doc.faq.length ? add : undefined} />;
  if (blocked)
    return (
      <>
        {head}
        {notes}
        {blocked}
      </>
    );
  return (
    <>
      {head}
      {notes}
      {doc.faq.length ? (
        <OrderedList
          label="Questions"
          items={doc.faq}
          readOnly={readOnly}
          nameOf={(f) => f.question}
          render={(f) => (
            <span className="min-w-0">
              <span className="block font-semibold text-ink">{f.question}</span>
              <span className="line-clamp-2 block text-sm text-ink-soft">{f.answer}</span>
            </span>
          )}
          onEdit={(f) => setOpen({ item: f })}
          onRemove={(f) => remove({ ...doc, faq: doc.faq.filter((x) => x !== f) }, `Removed “${f.question}”`)}
          onReorder={async (faq) => void (await persist({ ...doc, faq }))}
          foot={`${doc.faq.length} of 30 questions`}
        />
      ) : readOnly ? (
        <p className="mt-4 text-ink-soft">No questions.</p>
      ) : (
        <EmptyState title="No questions yet" action={add}>
          Answer what people ask most: who can join, where it happens, what to hand in.
        </EmptyState>
      )}
      {open && <FaqDialog p={p} existing={open.item} onClose={() => setOpen(null)} />}
    </>
  );
}
