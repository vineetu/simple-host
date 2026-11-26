// The data every frame reads, one hook each (TanStack Query keys are shared,
// so a screen reading the same thing gets the cached answer).
import { useQuery } from "@tanstack/react-query";
import { useHack } from "@/lib/client";

export function useMe() {
  const api = useHack();
  return useQuery({ queryKey: ["me"], queryFn: () => api.me.get(), staleTime: 60_000 });
}

export function useEvents() {
  const api = useHack();
  return useQuery({ queryKey: ["events"], queryFn: () => api.hack.events(), refetchOnWindowFocus: true });
}

export function useEventDetail(slug: string | undefined) {
  const api = useHack();
  return useQuery({ queryKey: ["event", slug], queryFn: () => api.event.get(slug!), enabled: !!slug, refetchOnWindowFocus: true });
}
