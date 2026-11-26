// The loading and failed states every screen shares.
import { Button } from "@kit/components/ui/button";
import { EmptyState } from "@kit/components/ui/empty-state";
import { Skeleton } from "@kit/components/ui/skeleton";

/** Loading is the place a screen's content will take, so nothing jumps. */
export function Loading() {
  return (
    <div className="mt-8" role="status" aria-busy="true" aria-label="Loading">
      <Skeleton className="h-8 w-56" />
      <Skeleton className="mt-6 h-24 w-full" />
      <Skeleton className="mt-3 h-24 w-full" />
    </div>
  );
}

/** Failed is a load that did not work, with Try again. */
export function Failed({ error, retry }: { error: unknown; retry: () => void }) {
  return (
    <EmptyState title="This did not load" action={<Button onClick={retry}>Try again</Button>}>
      {(error as Error)?.message || "Something went wrong."}
    </EmptyState>
  );
}

