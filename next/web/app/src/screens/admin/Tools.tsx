import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Input, Label } from "@/components/ui/input";
import { Section } from "@/components/ui/page-shell";
import { useActions } from "./actions";

/** Tools: on a self-hosted install the operator makes an account for someone. */
export function ToolsTab() {
  const act = useActions();
  const [email, setEmail] = useState("");
  const [busy, setBusy] = useState(false);
  return (
    <Section title="Make an account" note="They sign in with a code sent to this email and choose their handle.">
      <form
        className="mt-2.5 flex flex-wrap items-end gap-2.5"
        onSubmit={async (e) => {
          e.preventDefault();
          setBusy(true);
          if (await act.makeAccount(email.trim())) setEmail("");
          setBusy(false);
        }}
      >
        <div className="min-w-0 flex-[1_1_260px]">
          <Label htmlFor="make-email">Email</Label>
          <Input id="make-email" type="email" required autoComplete="off" placeholder="name@example.com" value={email} onChange={(e) => setEmail(e.target.value)} />
        </div>
        <Button type="submit" variant="primary" className="min-h-12 px-6" disabled={busy}>
          Make account
        </Button>
      </form>
    </Section>
  );
}
