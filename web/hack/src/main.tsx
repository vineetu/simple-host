// Simple Hack's app: one bundle for every app address the Go server sends
// here (products/hack/pages/hack_ui.go). One route (app/routes.tsx);
// app/Screen.tsx works out which screen the address is and its frame.
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { createBrowserRouter, RouterProvider } from "react-router";
import "./styles/app.css";
import { Providers } from "./app/Providers";
import { routes } from "./app/routes";
import { startSSO } from "./lib/session";

startSSO();
const router = createBrowserRouter(routes);

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <Providers>
      <RouterProvider router={router} />
    </Providers>
  </StrictMode>,
);
