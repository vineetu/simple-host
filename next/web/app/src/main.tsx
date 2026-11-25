import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter, Route, Routes } from "react-router";
import { registerSW } from "virtual:pwa-register";
import "./styles/app.css";
import { Providers } from "./App";
import { SignIn } from "./screens/signin/SignIn";
import { Admin } from "./screens/admin/Admin";

// The owner app: one bundle for the pages the server sends here (/signin, /admin).
function NotHere() {
  window.location.replace("/");
  return null;
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <Providers>
      <BrowserRouter>
        <Routes>
          <Route path="/signin" element={<SignIn />} />
          <Route path="/admin" element={<Admin />} />
          <Route path="*" element={<NotHere />} />
        </Routes>
      </BrowserRouter>
    </Providers>
  </StrictMode>,
);

if (import.meta.env.PROD) registerSW({ immediate: true });
