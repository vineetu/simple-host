import { cpSync, readdirSync, readFileSync, existsSync, mkdirSync, statSync } from "node:fs";
import { fileURLToPath, URL } from "node:url";
import { defineConfig, type PluginOption } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// Two builds from one source:
//   vite build -c hack/vite.config.ts                   the Simple Hack app into products/hack/web/dist/files (embedded by Go)
//   vite build -c hack/vite.config.ts --mode catalogue  the screen catalogue into hack/.catalogue (for the visual checks)
// Paths are from this folder (web/hack), whatever the working directory.
const here = fileURLToPath(new URL(".", import.meta.url));
const at = (p: string) => fileURLToPath(new URL(p, import.meta.url));

export default defineConfig(({ mode }) => {
  const catalogue = mode === "catalogue";
  const plugins: PluginOption[] = [react(), tailwindcss()];
  if (!catalogue) plugins.push(liveScreens());
  if (catalogue) plugins.push(serverPages());
  // The two stylesheets of the pages the Go server writes (no React): the
  // public event page and the chrome pages. Fixed names; the server adds ?v=.
  const pageStyles = { "hack-event": at("src/styles/hack-event.css"), "hack-pages": at("src/styles/hack-pages.css") };
  const styleName = (a: { names?: string[] }, dflt: string) =>
    a.names?.[0] === "hack-event.css" || a.names?.[0] === "hack-pages.css" ? a.names[0] : dflt;
  return {
    root: here,
    plugins,
    resolve: { alias: { "@kit": at("../kit"), "@": at("./src") } },
    build: catalogue
      ? {
          outDir: ".catalogue",
          emptyOutDir: true,
          rollupOptions: {
            input: { catalogue: at("catalogue.html"), ...pageStyles } as Record<string, string>,
            output: { assetFileNames: (a) => styleName(a, "assets/[name]-[hash][extname]") },
          },
        }
      : {
          // SH_WEB_OUT: build elsewhere (the stale-build check compares the two).
          outDir: process.env.SH_WEB_OUT || "../../products/hack/web/dist/files",
          emptyOutDir: true,
          assetsDir: "hack/app",
          reportCompressedSize: true,
          rollupOptions: {
            input: { index: at("index.html"), ...pageStyles } as Record<string, string>,
            output: { assetFileNames: (a) => styleName(a, "hack/app/[name]-[hash][extname]") },
          },
        },
  };
});

/**
 * liveScreens writes screens.json beside index.html: the screen ids this
 * build serves, merged from every src/screens/<area>/live.json. The Go server
 * reads it (products/hack/pages/hack_ui.go) to choose, per request, between
 * this app and the old one; nothing else needs editing to switch a screen.
 */
function liveScreens(): PluginOption {
  return {
    name: "live-screens",
    generateBundle() {
      const dir = at("src/screens");
      const ids: string[] = [];
      for (const area of readdirSync(dir).sort()) {
        const f = `${dir}/${area}/live.json`;
        if (existsSync(f)) ids.push(...(JSON.parse(readFileSync(f, "utf8")) as string[]));
      }
      if (new Set(ids).size !== ids.length) this.error("a screen id is in two live.json files");
      this.emitFile({ type: "asset", fileName: "screens.json", source: JSON.stringify({ live: ids.sort() }, null, 1) + "\n" });
    },
  };
}

/**
 * serverPages puts the pages the Go server writes into the catalogue build:
 * `make hack-web-pages` renders them (with every file they load) into
 * .catalogue-pages; the pages go under /catalogue-pages/, their files at the
 * root where the pages ask for them. The two stylesheets are this build's.
 */
function serverPages(): PluginOption {
  return {
    name: "server-pages",
    closeBundle() {
      const src = at(".catalogue-pages");
      if (!existsSync(src)) {
        console.warn("no server pages in the catalogue: run make hack-web-pages");
        return;
      }
      const out = at(".catalogue");
      mkdirSync(`${out}/catalogue-pages`, { recursive: true });
      for (const f of readdirSync(src)) {
        const from = `${src}/${f}`;
        if (f.endsWith(".html") && statSync(from).isFile()) cpSync(from, `${out}/catalogue-pages/${f}`);
        else if (f !== "hack-event.css" && f !== "hack-pages.css") cpSync(from, `${out}/${f}`, { recursive: true });
      }
    },
  };
}
