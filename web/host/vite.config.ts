import { cpSync, existsSync, mkdirSync } from "node:fs";
import { fileURLToPath, URL } from "node:url";
import { defineConfig, type PluginOption } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { VitePWA } from "vite-plugin-pwa";

// Two builds from one source:
//   vite build -c host/vite.config.ts                   the owner app and pages.css into next/web/dist/files (embedded by Go)
//   vite build -c host/vite.config.ts --mode catalogue  the screen catalogue into host/.catalogue (for the visual checks)
// Paths are from this folder (web/host), whatever the working directory.
const here = fileURLToPath(new URL(".", import.meta.url));
const at = (p: string) => fileURLToPath(new URL(p, import.meta.url));

export default defineConfig(({ mode }) => {
  const catalogue = mode === "catalogue";
  const plugins: PluginOption[] = [react(), tailwindcss()];
  if (catalogue) plugins.push(serverPages());
  if (!catalogue) {
    plugins.push(
      VitePWA({
        injectRegister: false, // main.tsx registers it, so no inline script is needed
        registerType: "autoUpdate",
        filename: "sw.js",
        manifest: {
          name: "Simple Host",
          short_name: "Simple Host",
          description: "Your websites on Simple Host.",
          start_url: "/dashboard",
          scope: "/",
          display: "standalone",
          background_color: "#f6f4e9",
          theme_color: "#f6f4e9",
          icons: [
            { src: "/app/icon-192.png", sizes: "192x192", type: "image/png" },
            { src: "/app/icon-512.png", sizes: "512x512", type: "image/png" },
            { src: "/app/icon-maskable-512.png", sizes: "512x512", type: "image/png", purpose: "maskable" },
          ],
        },
        workbox: {
          // The app shell is cached for offline only: signing in, the dashboard and the admin page
          // always load from the service, and open from the cached shell when it cannot be reached
          // (they then say so). Serving them from the cache first showed the build before the last
          // deploy until every tab closed. A new worker takes over at once; it never reloads a page.
          // Browser push comes from the existing push worker, so one worker covers the whole origin.
          globPatterns: ["index.html", "app/*.{js,css}", "app/fonts/*.woff2"],
          navigateFallback: null,
          // Only the exact files are answered from the cache. Without this, a visit to "/" was
          // answered with the cached app shell (as "/index.html"), which sent the browser to "/" again, for ever.
          directoryIndex: null,
          runtimeCaching: [
            {
              urlPattern: ({ request, url }) => request.mode === "navigate" && /^\/(signin|admin|dashboard)(\/|$)/.test(url.pathname),
              handler: "NetworkOnly",
              options: { precacheFallback: { fallbackURL: "/index.html" } },
            },
          ],
          skipWaiting: true,
          clientsClaim: true,
          importScripts: ["/push-sw.js"],
          inlineWorkboxRuntime: true,
          cleanupOutdatedCaches: true,
        },
      }),
    );
  }
  return {
    root: here,
    plugins,
    resolve: { alias: { "@kit": at("../kit"), "@": at("./src") } },
    build: catalogue
      ? {
          outDir: ".catalogue",
          emptyOutDir: true,
          rollupOptions: {
            input: { catalogue: at("catalogue.html"), pages: at("src/styles/pages.css") } as Record<string, string>,
            output: { assetFileNames: (a) => (a.names?.[0] === "pages.css" ? "pages.css" : "assets/[name]-[hash][extname]") },
          },
        }
      : {
          // SH_WEB_OUT: build elsewhere (the stale-build check compares the two).
          outDir: process.env.SH_WEB_OUT || "../../next/web/dist/files",
          emptyOutDir: true,
          assetsDir: "app",
          reportCompressedSize: true,
          rollupOptions: {
            input: { index: at("index.html"), pages: at("src/styles/pages.css") },
            output: {
              assetFileNames: (a) => (a.names?.[0] === "pages.css" ? "pages.css" : "app/[name]-[hash][extname]"),
            },
          },
        },
  };
});

/**
 * serverPages puts the pages the Go server writes into the catalogue build,
 * under /catalogue-pages/: the public text pages as they are, and the gate pages
 * that `make web-pages` rendered into .catalogue-pages (with a link to the
 * pages.css built here instead of their inline copy).
 */
function serverPages(): PluginOption {
  return {
    name: "server-pages",
    closeBundle() {
      const out = at(".catalogue/catalogue-pages");
      const statics = at("../../next/web/static");
      mkdirSync(out, { recursive: true });
      for (const p of ["privacy", "terms", "support", "docs", "features"]) cpSync(`${statics}/${p}.html`, `${out}/${p}.html`);
      cpSync(`${statics}/header.js`, at(".catalogue/header.js")); // the text pages' account corner
      mkdirSync(at(".catalogue/assets"), { recursive: true });
      cpSync(`${statics}/assets/favicon.svg`, at(".catalogue/assets/favicon.svg"));
      cpSync(at("public/app/icon-192.png"), at(".catalogue/favicon.ico")); // what a browser asks a gate page's host for
      if (existsSync(at(".catalogue-pages"))) cpSync(at(".catalogue-pages"), out, { recursive: true });
      else console.warn("no gate pages in the catalogue: run make web-pages");
    },
  };
}
