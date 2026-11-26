// Rubric & brief (/e/{slug}/judge?s=rubric): what each score means, read any
// time, even before judging opens. The criteria and their points, then the
// event's About, Rules and Prizes, and "I've read it", which records the
// rubric version on screen (an edit to the rubric asks again).
import { Badge } from "@kit/components/ui/badge";
import { EmptyState } from "@kit/components/ui/empty-state";
import { PageTitle, Section } from "@kit/components/ui/page-shell";
import { Skeleton } from "@kit/components/ui/skeleton";
import { useEventDetail } from "@/app/data";
import { Failed } from "@/app/states";
import type { ScreenProps } from "@/routes/registry";
import { paragraphs, rubricMax } from "./data";
import { Acknowledge } from "./Checklist";
import { useRubric } from "./queries";

export default function Rubric({ match }: ScreenProps) {
  const slug = match.slug!;
  const d = useEventDetail(slug).data!;
  const q = useRubric(slug);
  const title = <PageTitle sub="What each score means.">Rubric &amp; brief</PageTitle>;
  if (q.isPending)
    return (
      <>
        {title}
        <div className="mt-6" role="status" aria-busy="true" aria-label="Loading the rubric">
          <Skeleton className="h-40 w-full" />
          <Skeleton className="mt-4 h-32 w-full" />
        </div>
      </>
    );
  if (q.isError)
    return (
      <>
        {title}
        <Failed error={q.error} retry={() => q.refetch()} />
      </>
    );
  const criteria = q.data.criteria || [];
  const ev = d.event;
  const parts = (
    [
      ["About this event", paragraphs(ev.about)],
      ["Rules", paragraphs(ev.rules)],
      ["Prizes", paragraphs(ev.prizes)],
    ] as const
  ).filter(([, p]) => p.length);
  return (
    <>
      {title}
      {criteria.length ? (
        <Section title="Rubric" note={`${criteria.length} ${criteria.length === 1 ? "criterion" : "criteria"} · ${rubricMax(criteria)} points total`} className="rounded-wobble border border-rule bg-card px-4 py-3">
          <ul className="m-0 list-none p-0">
            {criteria.map((c) => (
              <li key={c.id} className="border-b border-rule py-3 last:border-b-0">
                <p className="flex flex-wrap items-baseline gap-2">
                  <span className="font-semibold">{c.name}</span>
                  <Badge>{Number(c.max_points) || 0} points</Badge>
                </p>
                {c.description && <p className="mt-0.5 text-sm text-ink-soft">{c.description}</p>}
              </li>
            ))}
          </ul>
          <Acknowledge d={d} id="j.rubric" label="I've read it" version={q.data.version} onStale={() => void q.refetch()} />
        </Section>
      ) : (
        <EmptyState title="No rubric yet">The organiser hasn’t published a rubric yet. Check back closer to judging.</EmptyState>
      )}
      {parts.length > 0 && (
        <section className="mt-7 rounded-wobble border border-rule bg-card px-4 py-3" aria-label="About this event">
          {parts.map(([h, p], i) => (
            <div key={h} className={i ? "mt-4 border-t border-rule pt-4" : ""}>
              <h2 className="type-section">{h}</h2>
              {p.map((t, k) => (
                <p key={k} className="mt-2 max-w-prose">
                  {t}
                </p>
              ))}
            </div>
          ))}
        </section>
      )}
    </>
  );
}
