# Validate, package, upload, verify

The inline JSON endpoint accepts text in `files` and base64-encoded binary in
`files_base64`, both keyed by relative site path. Do not put one path in both maps.

Run every check below on **the directory you are about to upload**. For a
framework project that is the build output (`dist/`, `build/`, `out/`, `public/`,
`.output/public/`) — not the project root.

## Pre-flight: mechanical

- Reject an empty directory.
- Require `index.html` at the directory root.
- Warn if the directory exceeds 100 MB. The API rejects archives over 100 MB.
- Warn if `node_modules/` is present — that usually means the source tree was
  selected instead of the built output.
- Warn if any `.env` file is present. It should not be uploaded.
- Warn on any single file over 25 MB.
- **Windows BOM:** PowerShell 5.1's `Set-Content` / `Out-File -Encoding utf8`
  prepends a UTF-8 BOM. Harmless in HTML, but it silently breaks a `.json`
  (strict `JSON.parse`), a `.css` `@charset`, and an ES-module `.js`. Author text
  files BOM-free: `-Encoding utf8NoBOM` on PowerShell 7+, or on 5.1
  `[System.IO.File]::WriteAllText($path, $content, (New-Object System.Text.UTF8Encoding($false)))`.

## Pre-flight: semantic

- If `package.json` has a build script and you are about to upload the project
  root, stop and build instead.
- For React, Vue, Next.js, Svelte, Astro, Nuxt, Gatsby, or Angular source trees:
  upload the built output, not the source.
- Flag server-side entrypoints (`server.js`, `app.py`, Express, Next.js API
  routes, Nuxt server handlers). Nothing runs server-side here.
- Flag absolute filesystem paths in HTML (`/Users/...`, `C:\...`, `file:///...`).
- Flag root-absolute asset links in HTML/CSS/JS (`href="/css/..."`,
  `src="/assets/..."`, `url(/fonts/...)`). These break under the path model.
- Flag case mismatches between HTML references and real filenames — works on
  macOS, breaks on Linux.

If anything blocks deployment, explain it and stop **before** uploading.

## Package

1. Confirm this is the final static directory.
2. Validate the sitename: lowercase letters, numbers, hyphens.
3. Archive into the OS temp directory — not a hardcoded `/tmp`, which does not
   exist on Windows:
   - macOS/Linux: `tar -czf /tmp/<sitename>.tar.gz -C <dir> .`
   - Windows PowerShell: `tar -czf $env:TEMP\<sitename>.tar.gz -C <dir> .`
     (bsdtar, bundled since Win10 1803), or
     `Compress-Archive -Path <dir>\* -DestinationPath $env:TEMP\<sitename>.zip`
   Files go at the archive **root** — do not wrap them in an extra directory.

## Upload

New site:

```
POST /v1/sites/<sitename>
X-API-Key: <api_key>
Content-Type: application/gzip
<binary archive body>
```

Existing site: same body, `PUT /v1/sites/<sitename>`. Either creates a new
version and activates it.

The response includes `active_version` and `site_url`.

## Verify — this step is not optional

1. The public URL is `https://sites.simple-host.app/<handle>/<sitename>/`, also
   returned as `site_url`.
2. Open it. Confirm the entrypoint renders and that assets, navigation, and
   styling are intact. Check for 404s in the network panel — broken CSS or JS
   almost always means root-absolute links.
3. If it is broken:
   - Source uploaded instead of build output → upload the build output.
   - Root-absolute asset paths → rebuild with a relative base and re-upload.
   - Case mismatch between references and filenames → fix and re-upload.

Do not report success from the upload response alone.
