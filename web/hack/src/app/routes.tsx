// The app's one route (app/Screen.tsx works out which screen the address is),
// with the unsaved-changes guard beside it. A data router (createBrowserRouter
// in main.tsx, createMemoryRouter in the catalogue), so the guard can hold
// Back and Forward too.
import type { RouteObject } from "react-router";
import { UnsavedGuard } from "@kit/guard/unsaved";
import { Screen } from "./Screen";

export const routes: RouteObject[] = [
  {
    path: "*",
    element: (
      <>
        <Screen />
        <UnsavedGuard />
      </>
    ),
  },
];
