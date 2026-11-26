// Your data: a download of everything (by email), signing out here or
// everywhere, and, set apart, deleting the account (typed email to confirm).
import { useState } from "react";
import { useApi } from "@/lib/api";
import { Button } from "@kit/components/ui/button";
import { AskDialog, Dialog, DialogActions } from "@kit/components/ui/dialog";
import { Field, Input } from "@kit/components/ui/input";
import { useToast } from "@kit/components/ui/toast";
import type { Me } from "../data";
import { Part } from "../bits";

export function YourData({ me }: { me: Me }) {
  const call = useApi();
  const toast = useToast();
  const [everywhere, setEverywhere] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const signOut = async (all: boolean) => {
    try {
      await call("POST", "/v1/auth/signout", all ? { everywhere: true } : {});
      window.location.replace("/");
    } catch (e) {
      toast((e as Error).message, { bad: true });
    }
  };
  return (
    <>
      <Part
        title="Download everything"
        note="Every site of yours and of your teams, with its files and saved data, and your account details, as one .zip. We email you a link when it is ready; the link works for 7 days."
        className="mt-2"
      >
        <Button
          variant="primary"
          className="px-6"
          onClick={async () => {
            try {
              const r = await call<{ says?: string }>("POST", "/v1/me/export", {});
              toast(r.says || "We are making it. You will get an email with a link when it is ready.");
            } catch (e) {
              toast((e as Error).message, { bad: true });
            }
          }}
        >
          Email me a download
        </Button>
      </Part>
      <Part title="Sign out" note={"Signed in as " + me.email + "."}>
        <div className="flex flex-wrap gap-2">
          <Button onClick={() => signOut(false)}>Sign out</Button>
          <Button onClick={() => setEverywhere(true)}>Sign out everywhere</Button>
        </div>
      </Part>
      <Part danger title="Delete your account" className="mt-12" note="Deletes every site of yours with its saved data, your keys and app connections, at once. Teams you are the only member of go too, with their sites. It cannot be undone.">
        <Button variant="danger" onClick={() => setDeleting(true)}>
          Delete my account…
        </Button>
      </Part>
      <AskDialog
        open={everywhere}
        onOpenChange={setEverywhere}
        title="Sign out everywhere?"
        body="Every browser, including this one, is signed out, and every connected AI app is disconnected. Keys keep working."
        confirmLabel="Sign out everywhere"
        onConfirm={() => signOut(true)}
      />
      <DeleteAccount me={me} open={deleting} onOpenChange={setDeleting} />
    </>
  );
}

function DeleteAccount({ me, open, onOpenChange }: { me: Me; open: boolean; onOpenChange: (o: boolean) => void }) {
  const call = useApi();
  const [typed, setTyped] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  return (
    <Dialog
      open={open}
      onOpenChange={(o) => (o || (setTyped(""), setError("")), onOpenChange(o))}
      title="Delete your account?"
      description={"Everything goes at once: " + me.use.sites + " sites with their saved data, your keys and app connections. It cannot be undone. A download first is a good idea."}
    >
      <form
        noValidate
        onSubmit={async (e) => {
          e.preventDefault();
          if (typed.trim().toLowerCase() !== me.email.toLowerCase()) return setError("Type " + me.email + " exactly, to show it is you.");
          setBusy(true);
          try {
            await call("DELETE", "/v1/me");
            window.location.replace("/");
          } catch (err) {
            setError((err as Error).message);
            setBusy(false);
          }
        }}
      >
        <Field id="confirm-email" label="Type your email to confirm" error={error}>
          <Input id="confirm-email" type="email" value={typed} onChange={(e) => (setTyped(e.target.value), setError(""))} autoComplete="off" aria-invalid={!!error} aria-describedby="confirm-email-msg" />
        </Field>
        <DialogActions>
          <Button onClick={() => onOpenChange(false)}>Cancel</Button>
          <Button type="submit" variant="danger" disabled={busy}>
            Delete my account
          </Button>
        </DialogActions>
      </form>
    </Dialog>
  );
}
