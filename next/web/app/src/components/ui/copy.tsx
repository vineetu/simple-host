import { useState } from "react";
import { Button, type ButtonProps } from "./button";

/** CopyButton copies text and says so on the button for a moment. */
export function CopyButton({ text, label = "Copy", done = "Copied", ...props }: { text: string; label?: string; done?: string } & ButtonProps) {
  const [state, setState] = useState<"" | "done" | "failed">("");
  return (
    <Button
      {...props}
      onClick={async () => {
        try {
          await navigator.clipboard.writeText(text);
          setState("done");
        } catch {
          setState("failed");
        }
        setTimeout(() => setState(""), 2000);
      }}
    >
      <span aria-live="polite">{state === "done" ? done : state === "failed" ? "Select and copy" : label}</span>
    </Button>
  );
}
