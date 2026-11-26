// The organiser's Ask panel (SIM-394; the old assistant.js): a floating Ask
// button on every organiser run screen and a panel beside the page (a sheet
// on a phone) that answers questions about this event in a line or two,
// with links to where to act. It never changes anything. Nothing shows when
// the server has no assistant set up (GET E/assistant enabled:false or an
// error), or for a platform admin's read-only look.
import { Fragment, useEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
import { useLocation } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { Dialog as D } from "radix-ui";
import { Button } from "@kit/components/ui/button";
import { CopyButton } from "@kit/components/ui/copy";
import { Textarea } from "@kit/components/ui/input";
import { cn } from "@kit/lib/utils";
import { paths, useHack } from "@/lib/client";
import { useEventDetail, useMe } from "@/app/data";
import { AppLink } from "@/app/nav";
import { chatFor } from "./assistant-chat";
import { blocks, MAX_MSG_CHARS, starterChips, storageKey, type Inline, type Status } from "./data-assistant";

type Checklist = Parameters<typeof starterChips>[1];

/** useAssistantStatus is GET E/assistant (shared with the walkthrough, which waits for it). */
export function useAssistantStatus(slug: string, enabled = true) {
  const api = useHack();
  return useQuery({ queryKey: ["assistant", slug], queryFn: () => api.assistant.status(slug) as Promise<Status>, enabled, retry: false, staleTime: 60_000 });
}

const phoneQuery = "(max-width: 699.98px)";
function usePhone() {
  return useSyncExternalStore(
    (fn) => {
      const m = window.matchMedia(phoneQuery);
      m.addEventListener("change", fn);
      return () => m.removeEventListener("change", fn);
    },
    () => window.matchMedia(phoneQuery).matches,
  );
}

function Inlines({ parts, onLink }: { parts: Inline[]; onLink: () => void }) {
  return (
    <>
      {parts.map((p, i) =>
        p.t === "a" ? (
          <AppLink key={i} to={p.href} onClick={onLink}>
            {p.text}
          </AppLink>
        ) : p.t === "code" ? (
          <code key={i} className="rounded-sm bg-paper-lo px-1 font-mono text-sm">
            {p.text}
          </code>
        ) : p.t === "strong" ? (
          <strong key={i}>{p.text}</strong>
        ) : p.t === "em" ? (
          <em key={i}>{p.text}</em>
        ) : (
          <Fragment key={i}>{p.text}</Fragment>
        ),
      )}
    </>
  );
}

/** Reply renders a reply's markdown safely (never as HTML). */
function Reply({ text, slug, onLink }: { text: string; slug: string; onLink: () => void }) {
  const bs = useMemo(() => blocks(text, slug), [text, slug]);
  return (
    <div className="flex flex-col gap-2">
      {bs.map((b, i) => {
        if (b.t === "pre")
          return (
            <pre key={i} className="overflow-x-auto rounded-field bg-paper-lo p-2 font-mono text-sm">
              <code>{b.text}</code>
            </pre>
          );
        if (b.t === "p")
          return (
            <p key={i}>
              {b.lines.map((l, j) => (
                <Fragment key={j}>
                  {j > 0 && <br />}
                  <Inlines parts={l} onLink={onLink} />
                </Fragment>
              ))}
            </p>
          );
        const L = b.t;
        return (
          <L key={i} className={cn("m-0 pl-5", L === "ul" ? "list-disc" : "list-decimal")}>
            {b.items.map((it, j) => (
              <li key={j}>
                <Inlines parts={it} onLink={onLink} />
              </li>
            ))}
          </L>
        );
      })}
    </div>
  );
}

export function Assistant({ slug, screen }: { slug: string; screen: string }) {
  const d = useEventDetail(slug).data;
  const eligible = !!d && !!d.can?.manage && !d.admin_view;
  const status = useAssistantStatus(slug, eligible);
  const me = useMe();
  if (!d || !eligible || status.data?.enabled !== true || me.isPending) return null;
  const key = storageKey(slug, me.data?.id || me.data?.username || null);
  return <Panel key={key} chatKey={key} slug={slug} screen={screen} status={status.data} stage={d.event.stage} checklist={d.checklist as Checklist} />;
}

function Panel({ chatKey, slug, screen, status, stage, checklist }: { chatKey: string; slug: string; screen: string; status: Status; stage: string; checklist: Checklist }) {
  const chat = useMemo(() => chatFor(chatKey, paths.assistant(slug)), [chatKey, slug]);
  useEffect(() => chat.load(status), [chat, status]);
  const v = useSyncExternalStore(chat.subscribe, chat.get);
  const phone = usePhone();
  const { search } = useLocation();
  const input = useRef<HTMLTextAreaElement>(null);
  const thread = useRef<HTMLDivElement>(null);
  const askBtn = useRef<HTMLButtonElement>(null);
  const [live, setLive] = useState("");
  // The screen and tab the organiser is on when a question goes out (the panel stays open across screens).
  const where = useRef({ screen, tab: "" });
  useEffect(() => {
    where.current = { screen, tab: new URLSearchParams(search).get("tab") || "" };
  }, [screen, search]);
  const at = () => where.current;

  useEffect(() => {
    const t = thread.current;
    if (t) t.scrollTop = t.scrollHeight;
  }, [v.messages, v.partial, v.error, v.streaming]);
  useEffect(() => {
    if (!v.said) return;
    const r = requestAnimationFrame(() => setLive(v.said));
    return () => cancelAnimationFrame(r);
  }, [v.said]);
  // After a turn, the box takes focus back (only when focus was on Send or nowhere).
  useEffect(() => {
    if (v.streaming || !v.open) return;
    const a = document.activeElement;
    if (a && a !== document.body && !a.hasAttribute("data-assistant-send")) return;
    input.current?.focus();
  }, [v.streaming, v.open]);
  // The box grows with the question.
  useEffect(() => {
    const ta = input.current;
    if (!ta) return;
    ta.style.height = "auto";
    ta.style.height = ta.scrollHeight + 2 + "px";
  }, [v.draft, v.open]);

  const close = () => {
    if (v.streaming) chat.stop();
    chat.setOpen(false);
  };
  const send = (text: string) => chat.ask(text.trim(), at);
  const followLink = () => phone && chat.setOpen(false);
  const empty = !v.messages.length && !v.streaming;
  const hint = v.limit
    ? v.limit
    : typeof v.remaining === "number" && v.remaining <= 5
      ? v.remaining + (v.remaining === 1 ? " question left today." : " questions left today.")
      : "";

  return (
    <>
      <Button
        ref={askBtn}
        data-tour="assistant"
        aria-expanded={v.open}
        aria-controls={"assistant-panel-" + slug}
        onClick={() => chat.setOpen(!v.open)}
        className="fixed right-4 bottom-[calc(var(--sh-bottom-bar,0px)+env(safe-area-inset-bottom)+16px)] z-30 rounded-tag px-5 md:right-6 md:bottom-6"
      >
        Ask
      </Button>
      <D.Root open={v.open} onOpenChange={(o) => (o ? chat.setOpen(true) : close())} modal={phone}>
        <D.Portal>
          {phone && <D.Overlay className="fixed inset-0 z-40 bg-ink/40" />}
          <D.Content
            id={"assistant-panel-" + slug}
            aria-describedby={undefined}
            onInteractOutside={(e) => {
              if (!phone) e.preventDefault();
            }}
            onEscapeKeyDown={(e) => {
              // Escape stops a reply first; a second Escape closes.
              if (v.streaming) {
                e.preventDefault();
                chat.stop();
              }
            }}
            onCloseAutoFocus={(e) => {
              e.preventDefault();
              askBtn.current?.focus();
            }}
            onOpenAutoFocus={(e) => {
              e.preventDefault();
              (input.current && !input.current.disabled ? input.current : askBtn.current)?.focus();
            }}
            className={cn(
              "fixed z-50 flex flex-col border-[1.5px] border-ink-soft bg-card text-ink shadow-pop",
              phone ? "inset-x-0 bottom-0 max-h-[85dvh] rounded-t-[14px] pb-[env(safe-area-inset-bottom)]" : "top-4 right-4 bottom-4 w-[380px] rounded-wobble",
            )}
          >
            <div className="flex items-center gap-2 border-b border-rule py-1 pr-1 pl-4">
              <D.Title className="mr-auto type-section">Ask about this event</D.Title>
              <Button variant="ghost" onClick={() => chat.reset()}>
                New chat
              </Button>
              <D.Close asChild>
                <Button variant="ghost" size="icon" aria-label="Close">
                  <span aria-hidden="true">✕</span>
                </Button>
              </D.Close>
            </div>
            {empty && <p className="px-4 pt-3 text-sm text-ink-soft">Short answers, with a link to where to act. It can&apos;t change anything.</p>}
            <div ref={thread} role="log" aria-label="Conversation" aria-busy={v.streaming} className="flex min-h-24 flex-1 flex-col gap-3 overflow-y-auto px-4 py-3">
              {v.messages.map((m, i) => {
                const isLast = i === v.messages.length - 1 && !v.streaming;
                return m.role === "user" ? (
                  <div key={i} className="flex flex-col items-end gap-1">
                    <p className="max-w-[85%] rounded-field bg-accent-soft px-3 py-2 whitespace-pre-wrap">{m.content}</p>
                    {isLast && !v.error && (
                      <Button variant="ghost" onClick={() => chat.retry(at)}>
                        Retry
                      </Button>
                    )}
                  </div>
                ) : (
                  <div key={i} className="flex flex-col items-start gap-1">
                    <div className="max-w-full rounded-field border border-rule bg-paper px-3 py-2">
                      <Reply text={m.content} slug={slug} onLink={followLink} />
                    </div>
                    {isLast && (
                      <div className="flex gap-2">
                        <CopyButton text={m.content} variant="ghost" />
                        <Button variant="ghost" onClick={() => chat.retry(at)}>
                          Retry
                        </Button>
                      </div>
                    )}
                  </div>
                );
              })}
              {v.streaming && (
                <div className="flex flex-col items-start">
                  <div className="max-w-full rounded-field border border-rule bg-paper px-3 py-2">
                    {v.partial ? <Reply text={v.partial} slug={slug} onLink={followLink} /> : <p className="text-ink-soft italic">Thinking…</p>}
                  </div>
                </div>
              )}
              {v.error && (
                <div role="alert" className="flex flex-wrap items-center gap-2 rounded-field border-[1.5px] border-bad px-3 py-1 text-sm text-bad">
                  <span className="min-w-0 flex-1 py-2">{v.error.text}</span>
                  {v.error.retry && (
                    <Button variant="ghost" onClick={() => chat.retry(at)}>
                      Retry
                    </Button>
                  )}
                </div>
              )}
              {empty && (
                <div role="group" aria-label="Questions to start with" className="flex flex-col items-start gap-2">
                  {starterChips(stage, checklist).map((c) => (
                    <button
                      key={c}
                      type="button"
                      disabled={!!v.limit}
                      onClick={() => send(c)}
                      className="min-h-11 cursor-pointer rounded-tag border-[1.5px] border-rule bg-card px-4 text-left text-sm text-ink hover:border-accent disabled:cursor-default disabled:opacity-55"
                    >
                      {c}
                    </button>
                  ))}
                </div>
              )}
            </div>
            <form
              className="border-t border-rule px-4 py-3"
              onSubmit={(e) => {
                e.preventDefault();
                if (v.streaming) chat.stop();
                else send(v.draft);
              }}
            >
              <div className="flex items-end gap-2">
                <label htmlFor={"assistant-input-" + slug} className="sr-only">
                  Your question
                </label>
                <Textarea
                  ref={input}
                  id={"assistant-input-" + slug}
                  rows={1}
                  maxLength={MAX_MSG_CHARS}
                  placeholder="Ask about this event…"
                  disabled={!!v.limit}
                  value={v.draft}
                  onChange={(e) => chat.setDraft(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing && e.keyCode !== 229) {
                      e.preventDefault();
                      if (!v.streaming) send(v.draft);
                    }
                  }}
                  className="max-h-40 min-h-11 resize-none py-2"
                />
                <Button type="submit" variant="primary" data-assistant-send="" disabled={!v.streaming && (!!v.limit || !v.draft.trim())}>
                  {v.streaming ? "Stop" : "Send"}
                </Button>
              </div>
              {hint && <p className={cn("mt-2 text-sm", v.limit ? "text-bad" : "text-ink-soft")}>{hint}</p>}
            </form>
            <div className="sr-only" role="status" aria-live="polite">
              {live}
            </div>
          </D.Content>
        </D.Portal>
      </D.Root>
    </>
  );
}
