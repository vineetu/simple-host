// Simple Hack's app: one bundle for every app address the Go server sends
// here (products/hack/pages/hack_ui.go). One route; app/Screen.tsx works out
// which screen the address is and its frame.
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter, Route, Routes } from "react-router";
import "./styles/app.css";
import { Providers } from "./app/Providers";
import { Screen } from "./app/Screen";
import { startSSO } from "./lib/session";

startSSO();

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <Providers>
      <BrowserRouter>
        <Routes>
          <Route path="*" element={<Screen />} />
        </Routes>
      </BrowserRouter>
    </Providers>
  </StrictMode>,
);
