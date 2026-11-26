// The judge screens' reads, one hook each, typed here (the client gives them
// untyped). Keys are shared, so moving between the four screens reuses them.
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useHack } from "@/lib/client";
import type { Checklist, Conflict, Queue, Rubric, Scores } from "./data";

export function useQueue(slug: string) {
  const api = useHack();
  return useQuery({ queryKey: ["judge-queue", slug], queryFn: async () => (await api.judging.queue(slug)) as unknown as Queue });
}

export function useMyConflicts(slug: string) {
  const api = useHack();
  return useQuery({ queryKey: ["conflicts-mine", slug], queryFn: async () => ((await api.judging.conflicts(slug, true)) as { conflicts?: Conflict[] }).conflicts || [] });
}

export function useRubric(slug: string) {
  const api = useHack();
  return useQuery({ queryKey: ["rubric", slug], queryFn: async () => (await api.judging.rubric(slug)) as unknown as Rubric });
}

export function useJudgeChecklist(slug: string) {
  const api = useHack();
  return useQuery({ queryKey: ["checklist", slug, "judge"], queryFn: async () => ((await api.event.checklist(slug, "judge")) as { checklist: Checklist }).checklist });
}

export function useMyScores(slug: string, team: string, enabled = true) {
  const api = useHack();
  return useQuery({ queryKey: ["judge-scores", slug, team], queryFn: async () => (await api.judging.scores(slug, team)) as unknown as Scores, enabled: enabled && !!team });
}

/** useRefreshJudging reloads what a score, a conflict or an acknowledgement changes. */
export function useRefreshJudging(slug: string) {
  const qc = useQueryClient();
  return () =>
    Promise.all(
      [["judge-queue", slug], ["conflicts-mine", slug], ["checklist", slug, "judge"], ["event", slug]].map((queryKey) => qc.invalidateQueries({ queryKey })),
    );
}
