import { PageTitle } from "@kit/components/ui/page-shell";
import type { ScreenProps } from "@/routes/registry";

export default function Settings({ match }: ScreenProps) {
  return <PageTitle data-slug={match.slug}>Settings</PageTitle>;
}
