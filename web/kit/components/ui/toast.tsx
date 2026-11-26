import { createContext, useCallback, useContext, useState, type ReactNode } from "react";
import { Toast as T } from "radix-ui";
import { cn } from "@kit/lib/utils";

type Undo = { label: string; run: () => void };
type Note = { id: number; text: string; bad: boolean; undo?: Undo };
type Opts = { bad?: boolean; undo?: Undo };
const ToastContext = createContext<(text: string, opts?: Opts) => void>(() => {});

/**
 * useToast returns toast(text, {bad, undo}): a short note at the bottom of the
 * screen. undo is a button that reverses what was just done (instead of asking first).
 * One note at a time: a new one replaces the one showing. On a phone it sits
 * just above the bottom bar (and the safe area); on a wide screen at the
 * bottom left of the content, beside the side rail (AppShell sets both offsets).
 */
export const useToast = () => useContext(ToastContext);

export function Toaster({ children }: { children: ReactNode }) {
  const [notes, setNotes] = useState<Note[]>([]);
  const toast = useCallback((text: string, opts?: Opts) => {
    setNotes([{ id: Date.now() + Math.random(), text, bad: !!opts?.bad, undo: opts?.undo }]);
  }, []);
  return (
    <ToastContext.Provider value={toast}>
      <T.Provider duration={6000} swipeDirection="down">
        {children}
        {notes.map((n) => (
          <T.Root
            key={n.id}
            duration={n.bad || n.undo ? 12000 : 6000}
            onOpenChange={(o) => !o && setNotes((all) => all.filter((x) => x.id !== n.id))}
            className={cn(
              "pointer-events-auto flex items-center gap-2 rounded-field border-[1.5px] bg-card py-1 pr-1 pl-4 shadow-pop",
              n.bad ? "border-bad text-bad" : "border-ink-soft text-ink",
            )}
          >
            <T.Description className="min-w-0 flex-1 py-2 text-sm">{n.text}</T.Description>
            {n.undo && (
              <T.Action altText={n.undo.label} asChild>
                <button
                  type="button"
                  onClick={n.undo.run}
                  className="min-h-11 flex-none cursor-pointer rounded-field px-3 text-base font-medium text-accent underline underline-offset-2 hover:text-accent-hover"
                >
                  {n.undo.label}
                </button>
              </T.Action>
            )}
            <T.Close aria-label="Dismiss" className="size-11 flex-none cursor-pointer rounded-field text-lg text-ink-soft hover:text-ink">
              ×
            </T.Close>
          </T.Root>
        ))}
        <T.Viewport className="pointer-events-none fixed bottom-[calc(var(--sh-bottom-bar,0px)+env(safe-area-inset-bottom)+12px)] left-[calc(var(--sh-rail,0px)+16px)] z-50 m-0 flex w-[min(420px,calc(100vw-var(--sh-rail,0px)-32px))] list-none flex-col p-0 outline-none md:left-[calc(var(--sh-rail,0px)+32px)] md:bottom-6" />
      </T.Provider>
    </ToastContext.Provider>
  );
}
