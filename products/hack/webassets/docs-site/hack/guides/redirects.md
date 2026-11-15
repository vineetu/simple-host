# Redirects and plain addresses

[What this is](/features.html#redirects-and-plain-addresses)

When moving a site, collect the old addresses and publish a root `_redirects` text file with the new files:

```text
# FROM [query conditions] TO [status]
/old /new 301
/blog/* /articles/:splat 301
/tour/:name /trips/:name 302
/ p=123 /articles/my-post/ 301!
/retired/* /gone.html 410
/missing /404.html 404!
```

Rules match top to bottom, ignoring trailing slashes. `:name` captures one segment; a final `*` captures the rest (including nothing), available as `:splat`. Query conditions must all match; other parameters are allowed. A literal condition such as `lang=en` matches that value exactly. Targets can use captured names in their path or query. An absolute HTTP(S) target must have a literal host.

301 is the default; 302 is temporary. Existing content shadows a rule unless its status ends in `!`. Use `301!` for old query links on a home page that still exists. Status 404 or 410 serves a local target page with that status; a missing target uses the site's `404.html`, or plain text. These are not rewrites or proxies; other status codes are unsupported.

Request query strings carry over only when the rule has no query conditions and the target has no query of its own. Local targets stay inside the site's public path on every address. Passcodes, named viewers, offline and take-down gates run before rules.

Team and event sites follow the same rules.

The file is validated before creating a version. Fix `redirects_invalid` errors at the listed line numbers and publish again. Limits are 128 KiB per file, 2048 bytes per line and 1000 rules by default; self-hosted operators can change `REDIRECTS_MAX_RULES`. Loops, and chains of more than 10 redirects in a row, are rejected. The file is never served to visitors; versions, file reads and exports retain it.

For plain addresses, `/faq` serves `faq.html` with 200 without changing the address. `/faq/` keeps working as before. When only `faq/index.html` exists, `/faq` keeps the 301 to `/faq/` so existing relative links continue to resolve. An exact file wins first, then `faq.html`, then the directory. No setting is needed.
