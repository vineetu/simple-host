# Detect a framework and build it for path hosting

Detect from `package.json` (dependencies and `scripts`) plus root config files.
Run the framework's **production** build with a **relative** base/public path,
then upload the output directory.

Sites live under `/<handle>/<sitename>/`, so root-absolute `/assets/...` breaks
and relative `./assets/...` works. Nothing runs at serve time — only static files
are served.

| Framework | Detect | Build (with relative base) | Output |
|---|---|---|---|
| Vite (Vue/React/Svelte/Preact/Lit) | `vite` dep or `vite.config.*` | `npx vite build --base ./` (or `base:'./'` in config) | `dist/` |
| Next.js | `next` dep or `next.config.*` | in `next.config`: `output:'export'`, `images.unoptimized:true`, `trailingSlash:true`, `assetPrefix:'./'` → `npx next build` | `out/` |
| Create React App | `react-scripts` dep | `PUBLIC_URL=. npm run build` (macOS/Linux/bash); PowerShell: `$env:PUBLIC_URL='.'; npm run build`; cmd.exe: `set PUBLIC_URL=.&& npm run build` | `build/` |
| SvelteKit | `@sveltejs/kit` dep or `svelte.config.*` | `@sveltejs/adapter-static` with `fallback:'index.html'` + `kit.paths.relative:true` → `npm run build` | `build/` |
| Astro | `astro` dep or `astro.config.*` | `base:'./'` in `astro.config` → `npx astro build` | `dist/` |
| Nuxt 3/4 | `nuxt` dep or `nuxt.config.*` | relative `app.baseURL` → `npx nuxt generate` (NOT `nuxt build` — that's a Node server) | `.output/public/` |
| Angular | `@angular/core` dep or `angular.json` | `ng build --configuration=production --base-href ./` | `dist/<proj>/` (or `dist/<proj>/browser/` on v17+) |
| Gatsby | `gatsby` dep or `gatsby-config.*` | relative `pathPrefix`/asset prefix → `npx gatsby build` | `public/` |
| Vue CLI (legacy, no Vite) | `@vue/cli-service` or `vue.config.js` | `publicPath:'./'` in `vue.config.js` → `npm run build` | `dist/` |
| Plain static (no build) | no `package.json` / no framework dep | none — upload as-is, **relative links only** | the dir itself |

**Unrecognized build system** (Eleventy, Hugo, Jekyll, Remix static export, Qwik,
SolidStart, VitePress, Docusaurus, …): run its normal production build with a
relative base/public path and upload the output directory. The rule does not
change — root-absolute asset URLs break under `/<handle>/<sitename>/`.

Never string-rewrite a built bundle to repair its base path. Rebuild with the
framework's own configuration.
