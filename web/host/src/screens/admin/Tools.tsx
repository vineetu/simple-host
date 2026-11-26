import { useState } from "react";
import { Button } from "@kit/components/ui/button";
import { FieldMessage, Input, Label } from "@kit/components/ui/input";
import { Section } from "@kit/components/ui/page-shell";
import { useActions } from "./actions";

/** Tools: on a self-hosted install the operator makes an account for someone. */
export function ToolsTab() {
  const act = useActions();
  const [email, setEmail] = useState("");
  const [busy, setBusy] = useState(false);
  const [bad, setBad] = useState("");
  return (
    <Section title="Make an account" note="They sign in with a code sent to this email and choose their handle.">
      <form
        noValidate
        className="mt-2.5 flex flex-wrap items-start gap-2.5"
        onSubmit={async (e) => {
          e.preventDefault();
          if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email.trim())) return setBad("Give a full email address, like name@example.com.");
          setBad("");
          setBusy(true);
          if (await act.makeAccount(email.trim())) setEmail("");
          setBusy(false);
        }}
      >
        <div className="min-w-0 flex-[1_1_260px]">
          <Label htmlFor="make-email">Email</Label>
          <Input
            id="make-email"
            type="email"
            autoComplete="off"
            placeholder="name@example.com"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            aria-invalid={!!bad || undefined}
            aria-describedby="make-msg"
          />
          <FieldMessage id="make-msg" tone="bad">
            {bad}
          </FieldMessage>
        </div>
        <Button type="submit" variant="primary" className="min-h-12 px-6 md:mt-[26px]" disabled={busy}>
          Make account
        </Button>
      </form>
    </Section>
  );
}
