import { PageTitle } from "@kit/components/ui/page-shell";
import type { ScreenProps } from "@/routes/registry";

export default function Judging({ match }: ScreenProps) {
  return <PageTitle data-slug={match.slug}>Judging</PageTitle>;
}
