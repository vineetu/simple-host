// Moving around: inside this app when the target screen is rebuilt and live,
// a full page load to the old app otherwise (routes/live.ts).
import type { ComponentProps } from "react";
import { Link, useNavigate } from "react-router";
import { hop, isHere } from "@/routes/live";
import { registered } from "@/routes/registry";

/** inApp is whether this app shows the screen at url. */
export const inApp = (url: string) => isHere(url, registered);

/** useGo returns go(url): navigate here, or load the old app's page. */
export function useGo() {
  const navigate = useNavigate();
  return (url: string) => (inApp(url) ? navigate(url) : void hop(url));
}

/** AppLink is a Link that reloads the page when the old app shows the target. */
export function AppLink({ to, ...props }: Omit<ComponentProps<typeof Link>, "to"> & { to: string }) {
  return <Link to={to} reloadDocument={!inApp(to)} {...props} />;
}
