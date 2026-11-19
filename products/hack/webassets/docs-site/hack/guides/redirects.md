# Redirects and plain addresses

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

## How page addresses work

[What this is](/features.html#redirects-and-plain-addresses)

When the program serves a page, `faq.html` lives at `/faq`. When there is no `faq/index.html`, a request for `/faq/` redirects to `/faq`. A folder page such as `faq/index.html` lives at `/faq/`. If only the folder page exists, `/faq` redirects to `/faq/`.

If both pages exist, `/faq` serves the file and `/faq/` serves the folder index. A folder needs an `index.html` to count as a page. An exact filename takes precedence over a sibling with an added `.html`. Explicit filenames such as `/faq.html`, `/faq/index.html` and `/style.css` work directly. A slash after a filename redirects to its address without the slash.

These redirects use 301, keep the query string and tell browsers to recheck the address. Redirect rules ignore trailing slashes when matching. Both spellings of a page take precedence over a rule unless its status ends in `!`. Rules are applied first and the slash redirect last, so it never joins a chain of rules. If you publish `faq.html` and there is no `faq/index.html`, you can remove the rule `/faq/ /faq`. The program already sends visitors there.
