// Every rebuilt screen, gathered from the route families: each
// screens/<area>/routes.ts exports `screens`, keyed by screen id (the ids
// resolveScreen gives). This file never needs editing: a new family's
// routes.ts is picked up by the glob. Whether a registered screen is served
// to people is the family's live.json (see live.ts).
import { lazy, type ComponentType, type LazyExoticComponent } from "react";
import type { Match } from "./resolve";

/** Where a screen sits: inside an event's frame, on the person's home level, or alone on a link page. */
export type ScreenKind = "event" | "home" | "public";
export type ScreenProps = { match: Match };
export type ScreenDef = {
  kind: ScreenKind;
  /** The browser tab's title before " · Simple Hack". */
  title: string;
  /** A title that follows the tab or the event (the old screen's title(ctx)), in place of title. */
  tabTitle?: (params: URLSearchParams, event: { title: string }) => string;
  load: () => Promise<{ default: ComponentType<ScreenProps> }>;
};

const families = import.meta.glob<{ screens: Record<string, ScreenDef> }>("../screens/*/routes.ts", { eager: true });

export const registry: Record<string, ScreenDef & { Component: LazyExoticComponent<ComponentType<ScreenProps>> }> = {};
for (const [file, mod] of Object.entries(families))
  for (const [id, def] of Object.entries(mod.screens)) {
    if (registry[id]) throw new Error(`screen ${id} is registered twice (${file})`);
    registry[id] = { ...def, Component: lazy(def.load) };
  }

export const registered = (id: string) => id in registry;
