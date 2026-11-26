// One chat per person and event, kept outside React (assistant.js's
// chatStates): moving to another organiser screen remounts the panel, and a
// reply still streaming, the half-typed question and whether the panel was
// open all carry over. The panel subscribes to it.
import { capMessages, clearHistory, cpLen, loadHistory, MAX_MSG_CHARS, readSSE, saveHistory, transport, type Msg, type Status, limitLine } from "./data-assistant";

const THROTTLED = "Something went wrong. Refresh the page and try again.";
const NO_ANSWER = "The assistant couldn't answer. Try again.";

export type ChatView = {
  messages: Msg[];
  streaming: boolean;
  /** The reply as it streams in ("" while thinking). */
  partial: string;
  remaining: number | null;
  limit: string;
  /** The error line under the thread, and whether it offers Retry. */
  error: { text: string; retry: boolean } | null;
  open: boolean;
  draft: string;
  /** The last full reply, for the screen reader's live line. */
  said: string;
};

export class Chat {
  private view: ChatView;
  private listeners = new Set<() => void>();
  private turn = 0;
  private abort: AbortController | null = null;
  private loaded = false;
  constructor(
    readonly key: string,
    private path: string,
  ) {
    this.view = { messages: [], streaming: false, partial: "", remaining: null, limit: "", error: null, open: false, draft: "", said: "" };
  }

  get = () => this.view;
  subscribe = (fn: () => void) => {
    this.listeners.add(fn);
    return () => void this.listeners.delete(fn);
  };
  private set(p: Partial<ChatView>) {
    this.view = { ...this.view, ...p };
    this.listeners.forEach((l) => l());
  }

  /** load takes the history and the status's counts the first time the panel mounts. */
  load(s: Status) {
    if (this.loaded) return;
    this.loaded = true;
    this.set({ messages: capMessages(loadHistory(this.key)), remaining: typeof s.remaining_today === "number" ? s.remaining_today : null, limit: this.view.limit || limitLine(s) });
  }

  setOpen = (open: boolean) => this.set({ open });
  setDraft = (draft: string) => this.set({ draft });

  private save(messages: Msg[]) {
    saveHistory(this.key, messages);
    return messages;
  }

  /** ask sends a question (with the screen and tab the organiser is on now). */
  ask(text: string, where: () => { screen: string; tab: string }) {
    if (!text || this.view.streaming) return;
    if (typeof navigator !== "undefined" && navigator.onLine === false) return this.set({ error: { text: "You're offline. Reconnect and try again.", retry: false } });
    const messages = this.save(capMessages([...this.view.messages, { role: "user", content: text }]));
    this.set({ messages, draft: "", error: null });
    void this.run(where);
  }

  /** retry asks the last question again (dropping the answer to it, if there is one). */
  retry(where: () => { screen: string; tab: string }) {
    if (this.view.streaming) return;
    if (typeof navigator !== "undefined" && navigator.onLine === false) return this.set({ error: { text: "You're offline. Reconnect and try again.", retry: true } });
    let messages = this.view.messages;
    if (messages.length && messages[messages.length - 1].role === "assistant") messages = messages.slice(0, -1);
    this.set({ messages: this.save(messages), error: null });
    void this.run(where);
  }

  stop = () => this.abort?.abort();

  /** reset is New chat: stop any reply, forget the history. */
  reset() {
    this.abort?.abort();
    this.turn++;
    this.abort = null;
    clearHistory(this.key);
    this.set({ messages: [], streaming: false, partial: "", error: null, said: "" });
  }

  private async run(where: () => { screen: string; tab: string }) {
    const mine = ++this.turn;
    const controller = new AbortController();
    this.abort = controller;
    this.set({ streaming: true, partial: "", error: null });
    const w = where();
    let res: Response;
    try {
      res = await transport.send(this.path, { messages: this.view.messages.map((m) => ({ role: m.role, content: m.content })), screen: w.screen, tab: w.tab }, controller.signal);
    } catch (e) {
      if (this.turn !== mine) return;
      this.set({ streaming: false, error: (e as Error)?.name === "AbortError" ? null : { text: "Cannot reach the server.", retry: true } });
      return;
    }
    if (!res.ok) {
      const data = (await res.json().catch(() => ({}))) as { error?: string; code?: string; remaining_today?: number };
      if (this.turn !== mine) return;
      let messages = this.view.messages;
      let popped = "";
      // A refused question (4xx) never stays in the history; a 400 puts it back in the box.
      if (res.status >= 400 && res.status < 500 && messages.length && messages[messages.length - 1].role === "user") {
        popped = messages[messages.length - 1].content;
        messages = this.save(messages.slice(0, -1));
      }
      const msg =
        res.status === 400 && cpLen(popped) > MAX_MSG_CHARS ? "That question is too long. Shorten it and try again." : (typeof data.error === "string" && data.error) || THROTTLED;
      const limited = data.code === "daily_limit" || data.code === "monthly_budget";
      this.set({
        messages,
        streaming: false,
        draft: res.status === 400 ? popped : this.view.draft,
        remaining: typeof data.remaining_today === "number" ? data.remaining_today : this.view.remaining,
        limit: limited ? msg : this.view.limit,
        error: { text: msg, retry: !limited },
      });
      return;
    }
    let acc = "";
    let done = false;
    let aborted = false;
    let failed = "";
    try {
      await readSSE(res.body!, (evt) => {
        if (this.turn !== mine) return;
        if (typeof evt.delta === "string") {
          acc += evt.delta;
          this.set({ partial: acc });
        } else if (evt.done) {
          done = true;
          if (typeof evt.remaining_today === "number") this.set({ remaining: evt.remaining_today });
        } else if (evt.error) failed = evt.message || NO_ANSWER;
      });
    } catch (e) {
      if ((e as Error)?.name === "AbortError") aborted = true;
      else failed = failed || "Cannot reach the server.";
    }
    if (this.turn !== mine) return;
    if (!done && !failed && !aborted) failed = NO_ANSWER;
    // An answer cut short still counted against the day.
    const remaining = !done && typeof this.view.remaining === "number" ? Math.max(0, this.view.remaining - 1) : this.view.remaining;
    const messages = acc.trim() ? this.save(capMessages([...this.view.messages, { role: "assistant", content: acc }])) : this.view.messages;
    this.abort = null;
    this.set({ messages, streaming: false, partial: "", remaining, error: failed ? { text: failed, retry: true } : null, said: acc.trim() && !aborted ? acc : "" });
  }
}

const chats = new Map<string, Chat>();
/** chatFor is the one chat for this storage key (created once, kept for the page's life). */
export function chatFor(key: string, path: string): Chat {
  let c = chats.get(key);
  if (!c) chats.set(key, (c = new Chat(key, path)));
  return c;
}
