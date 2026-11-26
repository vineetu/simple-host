// Moving around: inside this app when it has the target screen, a full page
// load to the page the server writes otherwise (routes/pages.ts).
import type { ComponentProps } from "react";
import { Link, useNavigate } from "react-router";
import { hop, isHere } from "@/routes/pages";
import { registered } from "@/routes/registry";

/** inApp is whether this app shows the screen at url. */
export const inApp = (url: string) => isHere(url, registered);

/** useGo returns go(url): navigate here, or load the server's page. */
export function useGo() {
  const navigate = useNavigate();
  return (url: string) => (inApp(url) ? navigate(url) : void hop(url));
}

/** AppLink is a Link that reloads the page when the server writes the target. */
export function AppLink({ to, ...props }: Omit<ComponentProps<typeof Link>, "to"> & { to: string }) {
  return <Link to={to} reloadDocument={!inApp(to)} {...props} />;
}
