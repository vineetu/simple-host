import { createContext, useCallback, useContext, useState, type ReactNode } from "react";
import { Toast as T } from "radix-ui";
import { cn } from "@/lib/utils";

type Undo = { label: string; run: () => void };
type Note = { id: number; text: string; bad: boolean; undo?: Undo };
type Opts = { bad?: boolean; undo?: Undo };
const ToastContext = createContext<(text: string, opts?: Opts) => void>(() => {});

/**
 * useToast returns toast(text, {bad, undo}): a short note at the bottom of the
 * screen. undo is a button that reverses what was just done (instead of asking first).
 */
export const useToast = () => useContext(ToastContext);

export function Toaster({ children }: { children: ReactNode }) {
  const [notes, setNotes] = useState<Note[]>([]);
  const toast = useCallback((text: string, opts?: Opts) => {
    setNotes((n) => [...n.slice(-2), { id: Date.now() + Math.random(), text, bad: !!opts?.bad, undo: opts?.undo }]);
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
              "flex items-start gap-3 rounded-tab border-[1.5px] bg-card px-4 py-3 shadow-pop",
              n.bad ? "border-bad text-bad" : "border-ink-soft text-ink",
            )}
          >
            <T.Description className="flex-1 pt-2.5">{n.text}</T.Description>
            {n.undo && (
              <T.Action altText={n.undo.label} asChild>
                <button
                  type="button"
                  onClick={n.undo.run}
                  className="-my-1 min-h-11 flex-none cursor-pointer rounded-field px-3 font-note text-note text-accent underline underline-offset-2 hover:text-accent-hover"
                >
                  {n.undo.label}
                </button>
              </T.Action>
            )}
            <T.Close aria-label="Dismiss" className="-my-1 -mr-2 size-11 flex-none cursor-pointer rounded-field text-lg text-ink-soft hover:text-ink">
              ×
            </T.Close>
          </T.Root>
        ))}
        <T.Viewport className="fixed right-0 bottom-[calc(56px+env(safe-area-inset-bottom))] z-50 m-0 flex md:bottom-0 w-full max-w-[420px] list-none flex-col gap-2 p-4 outline-none" />
      </T.Provider>
    </ToastContext.Provider>
  );
}
