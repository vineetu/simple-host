# Publishing: files, builds, versions, limits

## What to send

`publish_site` takes `files` (path to text) and `files_base64` (path to base64, for images,
fonts, media, `.wasm`). `index.html` goes at the root. Every file of a build goes in one call, or
the site goes up as an archive through `create_upload_link` with `kind: "archive"`. With
`keep_other_files: true` the live files you do not send stay; without it they are removed.

Simple Host serves static files only. A project with a build step is built first, where you can
run commands, and only the output folder is published (`dist/`, `build/`, `out/`, `public/`,
`.output/public/`). Use the framework's static output (Nuxt `generate`, Next `output: 'export'`,
SvelteKit `adapter-static`). Drop source maps, `node_modules`, `.env`, `.git` and private keys.
A site has its own address, so root-absolute links (`/css/site.css`) work. Client-side routing:
a hash router, or a `404.html` that boots the app. For plain multi-page sites give each page its
own folder with an `index.html`, or use `about.html`; `/about`, `/about/` and `/about.html` all
reach it.

## `_redirects`

One line per redirect: `old-path new-path 301` (or 302). A mistake is reported with its line
number and nothing is published.

## Versions

Every publish is a numbered version. A few recent ones are kept (four). `get_site` lists them;
`rollback_site` with `version` makes one live again (ask first). A deleted site stays in the
bin for 7 days with its data (`list_sites` with `bin: true`, then `restore_site`). Saved data and stored files survive publishes and version changes.

## Limits

- A site's published files: 100 MB. Saved data (tables and values): 100 MB. Stored files: 200 MB
  in all. A request over a limit is refused whole and the refusal names the limit.
- A saved-data query may run 2 seconds: add a `WHERE`, a `LIMIT` or an index.
- Keep zips, installers and videos elsewhere and link to them. Resize phone photos in the
  browser before upload (the photo picker does it).

## After publishing

Give the person the `url` the tool returned. If you can open it, check the page renders and its
styles and scripts load. Broken styling means a wrong link or a missing file: fix it and publish
again rather than reporting success.
