import { lazy, StrictMode, Suspense } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter, Route, Routes } from "react-router";
import "./styles/app.css";
import { Providers } from "./App";

// Each screen is its own file, fetched when it is opened: sign-in stays small.
const SignIn = lazy(() => import("./screens/signin/SignIn").then((m) => ({ default: m.SignIn })));
const Upload = lazy(() => import("./screens/upload/Upload").then((m) => ({ default: m.Upload })));
const Admin = lazy(() => import("./screens/admin/Admin").then((m) => ({ default: m.Admin })));
const Dashboard = lazy(() => import("./screens/dashboard/Dashboard").then((m) => ({ default: m.Dashboard })));

// The owner app: one bundle for the pages the server sends here (/signin, /admin, /dashboard, /upload/<token>).
function NotHere() {
  window.location.replace("/");
  return null;
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <Providers>
      <BrowserRouter>
        <Suspense fallback={null}>
        <Routes>
          <Route path="/signin" element={<SignIn />} />
          <Route path="/admin" element={<Admin />} />
          <Route path="/dashboard/*" element={<Dashboard />} />
          <Route path="/upload/:token" element={<Upload />} />
          <Route path="*" element={<NotHere />} />
        </Routes>
        </Suspense>
      </BrowserRouter>
    </Providers>
  </StrictMode>,
);

// The offline worker (vite.config.ts). A page left open across a deploy asks for a
// screen's file that is gone: load the page again, which brings the new build.
if (import.meta.env.PROD && "serviceWorker" in navigator) navigator.serviceWorker.register("/sw.js");
window.addEventListener("vite:preloadError", () => window.location.reload());
