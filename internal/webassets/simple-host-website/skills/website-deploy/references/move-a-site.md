# Moving an existing website to Simple Host (WordPress, Squarespace, Wix, any site)

Use this when someone asks to move, copy or migrate a website they already have ("move my
WordPress site", "keep everything"). The result is a static copy with the same pages, the same
look and the same paths, and every part that needed the old server replaced by Simple Host
storage, built in the same pass rather than offered later.

This needs a coding agent with a shell (Claude Code, Codex): crawling, converting and packing a
site with hundreds of files does not fit in chat tool calls. In a chat app without a shell, say
so plainly: a few pages can be rebuilt by hand there; for a whole site, open a coding app.

## 1. Look before you build, and say what will change

- List every page and post: `sitemap.xml` (or the sitemap index it names), and on WordPress the
  REST API: `/wp-json/wp/v2/pages?per_page=100` and `/wp-json/wp/v2/posts?per_page=100` (more
  pages with `&page=2`). For a WordPress.com site, the same data is at
  `https://public-api.wordpress.com/wp/v2/sites/<domain>/pages`, which is not rate limited the way
  the site itself is. Count tags and categories too; their archive pages are links on every post.
- Find every part that ran on the old server. Search the fetched HTML for forms and plugin names:
  contact and feedback forms (Jetpack `contact-form`, Contact Form 7, WPForms), comment forms
  (`commentform`, `respond`), guestbooks (`gwolle`), forums (`asgaros`, `bbpress`), search boxes
  (`role="search"`), subscribe boxes (`jetpack-subscriptions`), hit counters, likes, shops,
  members areas. A section with no entries yet still has a working form on the old site.
- Tell the person, in one short list, what comes over as it is, what is replaced and with what
  (table in step 4), and what cannot come over (the old editor and admin, old stats history,
  forum and member accounts). Then ask the one question the skill always asks before a first
  publish: the site name and address.

## 2. Crawl politely

- Read `robots.txt` first and obey it. Never pass `-e robots=off` or ignore its rules.
- One request at a time, a few seconds apart, with a normal browser user agent. On a 429 or a
  "slow down" page, wait a minute and try again; WordPress.com answers 429 after a short burst.
  The person's own site being slow for others is not acceptable.
- Fetch every page and post once, plus pagination, category, author and the pages the menus
  link. Tag archives can be built from the posts you already have (copy an archive page and fill
  it with the matching post excerpts) instead of fetching each one.
- Fetch images at the size the pages show, at most about 1600 px wide, keeping the aspect ratio.
  On WordPress.com images come from `i0.wp.com/<domain>/wp-content/uploads/...`; add `?w=1600`
  to get a web-sized copy (leave out a quality parameter on PNG files, or they come back as JPEG).
- Fetch the theme's CSS, fonts and the scripts the pages need to look and move the same (menus,
  slideshows, tiled galleries). On WordPress, `/_static/??-eJ...` URLs are bundles of several
  files; keep them as single files.

## 3. Convert

- Keep the paths: `/about/` becomes `about/index.html`, so every old link and bookmark still
  works. Make every link relative (`../../contact/`), never `/contact/`.
- Point every image, stylesheet and script at its local copy. Match a downloaded file by its path
  and query together: on WordPress.com `/?custom-css=...` is the theme's extra stylesheet and `/`
  is the home page, so a match that ignores the query sends Home and the site title to a CSS
  file. Only file paths (with an extension) are assets; a page link never maps to one. Rewrite
  the URLs in a `<script type="importmap">` too (to `./` or `../` paths), or the phone menu
  loads from the old server. Drop `srcset` entries you did not
  download, and drop the old site's tracking, stats, likes, related-posts and editor scripts:
  they call the old server and only produce errors.
- Replace the footer credit for the old platform with a plain one.
- WordPress repeats the same large inline `<style>` blocks on every page; moving each repeated
  block into one shared CSS file often halves the HTML.
- Check the total before publishing: a site may hold 200 MB including the live copy and its
  saved versions (about five copies), so keep a moved site under about 35 MB. Shrink photos and
  very large GIFs first.

## 4. Replace what needed the old server (in the same build)

Create each resource with its preset and tables before publishing the pages that use them, then
keep the old form's markup and classes so it looks the same, and save with `SH.storage`.

| Old part | Replacement | Topic |
|---|---|---|
| Contact, feedback, "submit to the archive" forms | the form saves to a table only the owner reads | `inbox` |
| Subscribe or newsletter box | the email saves to a `subscribers` table; tell the owner nothing is sent automatically | `inbox` |
| Comments under posts and pages | one `comments` resource, a `page` column, anyone posts after a spam check | `comments` (or `wall` for signed-in posting) |
| Guestbook | its own resource with the same fields (name, city, website, message) | `comments` |
| Forum | `topics` and `replies` tables, people sign in to post, authors delete their own | `wall` |
| Site search | a JSON index of every page and post built at conversion, searched in the browser on a `search/` page | none |
| Hit or visitor counter | drop it, or keep its text without a number; the owner reads visits with `site_analytics` | none |
| Shop, members area | `records` or `portal`; say what changes | `records`, `portal` |

Never fake a feature: a form that shows "sent" must have saved. Visitors' text is shown with
`textContent`, never as HTML.

## 5. Publish

- Small sites: `create_site` with the files inline.
- Larger sites (anything with many photos): pack the folder (`tar -czf site.tar.gz -C <folder> .`),
  call `create_upload_link` with `archive: true` and the site name, and run the one-line `curl` it returns. The link
  works once, for that site, for 15 minutes. The answer has `site_url`. Never put the link in a
  page or in chat for others.
- New sites start unlisted. Leave them that way while the person checks the copy.

## 6. Check, then hand over

- Every page answers 200, no image is broken, menus open on a phone, Home, the site title and
  every menu item open a page (not a `.css` or `.js` file), search finds a word from a
  post, and each form saves: send one test entry, read it back with `storage_sql_query`, then
  delete it with `storage_sql_execute` after saying so.
- Give the person the address, what was replaced, and how they read what comes in ("ask me:
  show my messages", "show the guestbook").

## 7. Their domain, when they are ready

`connect_domain` with their domain, then relay the two records exactly (`connect-domain`
skill). The domain stays registered where it is; only its DNS records change. On WordPress.com
(name servers `ns1.wordpress.com`): Hosting Dashboard, **Domains**, click the domain,
**DNS records**, **Add record**, type **A**, Name blank, "Points To" the IP from `connect_domain`;
then **Add record** again, type **TXT**, Name `_simple-host`, value the token. The new A record
replaces WordPress.com's default ones on its own. Suggest keeping the old site and plan until the
new one has run for a week.
