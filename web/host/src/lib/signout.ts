// Signing out of the owner app: the one place that does it. The session ends
// on the server, then the browser goes to the home page.
import { useApi } from "@/lib/api";
import { useToast } from "@kit/components/ui/toast";

export function useSignOut() {
  const call = useApi();
  const toast = useToast();
  return async (everywhere = false) => {
    try {
      await call("POST", "/v1/auth/signout", everywhere ? { everywhere: true } : {});
      window.location.replace("/");
    } catch (e) {
      toast((e as Error).message, { bad: true });
    }
  };
}
