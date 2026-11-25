import { fileURLToPath, URL } from "node:url";
import { defineConfig, type PluginOption } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { VitePWA } from "vite-plugin-pwa";

// Two builds from one source:
//   vite build                   the owner app and pages.css into ../dist/files (embedded by Go)
//   vite build --mode catalogue  the screen catalogue into .catalogue (for the visual checks)
export default defineConfig(({ mode }) => {
  const catalogue = mode === "catalogue";
  const plugins: PluginOption[] = [react(), tailwindcss()];
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
          // The app shell is cached; signing in, the dashboard and the admin
          // page open from it offline and say they cannot reach the service. Browser push comes from
          // the existing push worker, so one worker covers the whole origin.
          globPatterns: ["index.html", "app/*.{js,css}", "app/fonts/*.woff2"],
          navigateFallback: "/index.html",
          navigateFallbackAllowlist: [/^\/(signin|admin|dashboard)(\/|$|\?)/],
          importScripts: ["/push-sw.js"],
          inlineWorkboxRuntime: true,
          cleanupOutdatedCaches: true,
        },
      }),
    );
  }
  return {
    plugins,
    resolve: { alias: { "@": fileURLToPath(new URL("./src", import.meta.url)) } },
    build: catalogue
      ? { outDir: ".catalogue", emptyOutDir: true, rollupOptions: { input: "catalogue.html" } }
      : {
          // SH_WEB_OUT: build elsewhere (the stale-build check compares the two).
          outDir: process.env.SH_WEB_OUT || "../dist/files",
          emptyOutDir: true,
          assetsDir: "app",
          reportCompressedSize: true,
          rollupOptions: {
            input: { index: "index.html", pages: "src/styles/pages.css" },
            output: {
              assetFileNames: (a) => (a.names?.[0] === "pages.css" ? "pages.css" : "app/[name]-[hash][extname]"),
            },
          },
        },
  };
});
