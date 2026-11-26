import { createContext, useContext, useState, type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ApiProvider, type Call } from "@kit/api/call";
import { Toaster } from "@kit/components/ui/toast";
import { fetchBytes, hackCall, type Bytes } from "@/lib/call";

const BytesContext = createContext<Bytes>(fetchBytes);
/** useBytes fetches a file the key may read (a screenshot, an export). */
export const useBytes = () => useContext(BytesContext);

/** Providers wraps the app (or one catalogue entry) with its Call, Bytes, data client and toasts. */
export function Providers({ children, call, bytes = fetchBytes, client }: { children: ReactNode; call?: Call; bytes?: Bytes; client?: QueryClient }) {
  const [qc] = useState(() => client ?? new QueryClient({ defaultOptions: { queries: { refetchOnWindowFocus: false, retry: 1 } } }));
  // After a call that changes which events the person is in, the list is read again.
  const [real] = useState(() => call ?? hackCall(() => void qc.invalidateQueries({ queryKey: ["events"] })));
  return (
    <ApiProvider value={real}>
      <BytesContext.Provider value={bytes}>
        <QueryClientProvider client={qc}>
          <Toaster>{children}</Toaster>
        </QueryClientProvider>
      </BytesContext.Provider>
    </ApiProvider>
  );
}
