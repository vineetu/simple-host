import type { ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ApiProvider, fetchCall, type Call } from "@/lib/api";
import { Toaster } from "@kit/components/ui/toast";

/** Providers wraps a screen with its data client, its way to call the API and the toasts. */
export function Providers({ children, call = fetchCall, client }: { children: ReactNode; call?: Call; client?: QueryClient }) {
  return (
    <ApiProvider value={call}>
      <QueryClientProvider client={client ?? defaultClient}>
        <Toaster>{children}</Toaster>
      </QueryClientProvider>
    </ApiProvider>
  );
}

const defaultClient = new QueryClient({ defaultOptions: { queries: { refetchOnWindowFocus: false, retry: 1 } } });
