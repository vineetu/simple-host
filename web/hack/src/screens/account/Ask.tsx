// One confirm dialog for the Account screen's asks (the kit's AskDialog): the
// action runs on confirm; a refusal closes it and says so in a toast.
import { useState } from "react";
import { AskDialog } from "@kit/components/ui/dialog";
import { useToast } from "@kit/components/ui/toast";
import { accountErr, type Words } from "./data";

export type AskSpec = {
  title: string;
  body?: string;
  confirmLabel: string;
  danger?: boolean;
  run: () => Promise<unknown>;
  /** What the toast says when the call is refused without its own sentence. */
  failed?: string;
  /** The toast after it worked. */
  done?: string;
};
export type Ask = (spec: AskSpec) => void;

export function useAsk(w: Words | null): [Ask, React.ReactNode] {
  const toast = useToast();
  const [spec, setSpec] = useState<AskSpec | null>(null);
  const dialog = spec && (
    <AskDialog
      open
      onOpenChange={(o) => !o && setSpec(null)}
      title={spec.title}
      body={spec.body}
      confirmLabel={spec.confirmLabel}
      danger={spec.danger}
      onConfirm={async () => {
        try {
          await spec.run();
          if (spec.done) toast(spec.done);
        } catch (e) {
          if ((e as { status?: number }).status !== 401) toast(accountErr(e, w, spec.failed), { bad: true });
        }
      }}
    />
  );
  return [setSpec, dialog];
}
