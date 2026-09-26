<!-- Derived from simple-host-website/skills/website-deploy/references/frameworks.md and references/packaging-and-validation.md. Keep in step. -->

# Framework builds, local folders, and checking files before publishing

Load this when the site comes from a framework project or a folder on disk rather than
files you wrote in the conversation.

## Publish the build output, never the source

Nothing runs on the server, so a project with a build step must be built first (where you
can run commands), and only the output directory is published. Every file in it goes in the
publish call: text files in `files`, binary files (images, fonts, media, `.wasm`) base64 in
`files_base64`. Large builds may not fit in one call; keep sites lean (compress images,
drop source maps) and tell the person if a build is too big to send.

The same site can be served at a host root or under a path, so build with a **relative** base path:

| Framework | Detect | Build with a relative base | Output |
|---|---|---|---|
| Vite (Vue/React/Svelte/Preact/Lit) | `vite` dep or `vite.config.*` | `npx vite build --base ./` (or `base:'./'` in config) | `dist/` |
| Next.js | `next` dep | `next.config`: `output:'export'`, `images.unoptimized:true`, `trailingSlash:true`, `assetPrefix:'./'`, then `npx next build` | `out/` |
| Create React App | `react-scripts` dep | `PUBLIC_URL=. npm run build` | `build/` |
| SvelteKit | `@sveltejs/kit` dep | `@sveltejs/adapter-static` with `fallback:'index.html'` and `kit.paths.relative:true` | `build/` |
| Astro | `astro` dep | `base:'./'` in `astro.config`, `npx astro build` | `dist/` |
| Nuxt 3/4 | `nuxt` dep | relative `app.baseURL`, `npx nuxt generate` (not `nuxt build`, which makes a server) | `.output/public/` |
| Angular | `@angular/core` dep | `ng build --configuration=production --base-href ./` | `dist/<proj>/` (`browser/` on v17+) |
| Gatsby | `gatsby` dep | relative `pathPrefix` / asset prefix, `npx gatsby build` | `public/` |
| Vue CLI | `@vue/cli-service` | `publicPath:'./'` in `vue.config.js` | `dist/` |
| Plain static | no build | none; relative links only | the folder itself |

Other generators (Eleventy, Hugo, Jekyll, VitePress, Docusaurus...): run the normal production
build with a relative base, publish the output. Never string-rewrite a built bundle to fix its
base path; rebuild with the framework's own setting.

Client-side routing: use the framework's hash router, or ship a `404.html` that boots the app.
For plain multi-page sites, give each page its own folder with an `index.html`
(`about/index.html`, linked as `about/`).

## Checks before publishing

Stop and explain before publishing if any of these fail:

- `index.html` is at the root of what you send.
- No `node_modules/`, `.env`, `.git/`, private keys or other secrets. (Secret files are dropped
  by the server anyway, and source-script extensions such as `.sh .py .php .rb .go` are
  rejected.)
- No server entrypoints expected to run (`server.js`, `app.py`, API routes). They will not run.
- No root-absolute links in HTML, CSS or JS (`href="/css/..."`, `src="/assets/..."`,
  `url(/fonts/...)`) and no local filesystem paths (`/Users/...`, `C:\...`, `file:///`).
- Every referenced file exists with exactly the same letter case.
- Text files are UTF-8 without a byte-order mark (a BOM breaks `.json` and ES-module `.js`).
- Any single file over 25 MB, or a site over 100 MB, is too large; shrink it.

## After publishing

Give the person the `url` the tool returned. If you can open it, confirm the page renders and
styles and scripts load; broken styling almost always means a root-absolute link slipped
through. Fix and publish again (a new version) rather than reporting success on a broken page.
