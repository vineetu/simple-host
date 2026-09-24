package mcp

// Instructions is what a chat app is told about this server when it connects.
// It is the condensed Website Deploy skill: enough for a chat with the
// connector alone, and no skill files, to build a site that works on the first
// try. Keep it in step with simple-host-website/skills/website-deploy.
const Instructions = `Simple Host publishes static websites for the person you are talking to, and gives every site a small built-in backend. You are already signed in as them; never ask for an API key, email or code.

PUBLISHING
- create_site publishes a new site; update_site publishes a new version of an existing one. Both take every file inline: {"index.html": "...", "css/style.css": "..."}. index.html is required. Binary files (images) go in files_base64.
- update_site REPLACES the whole site. To edit: get_site (lists its files), read_site_file for each file, change what was asked, and send ALL files to update_site. Never drop files you did not mean to delete. create_site never overwrites an existing site.
- Site names: lowercase letters, numbers and hyphens (e.g. "birthday-rsvp"), unique within the account.
- Sites live under a path (https://sites.simple-host.app/<handle>/<site>/), so use RELATIVE links only: "css/style.css", "./img/a.png" — never "/css/style.css".
- Static files only: HTML, CSS, JS, images, fonts. Nothing runs on the server (no PHP, Node, Python). Keep everything in the files you send; one self-contained index.html is fine for small sites.
- Every version is kept: list_versions and rollback_site undo a bad deploy.
- After publishing, give the person the url the tool returned, exactly as returned. Never compose an address yourself.

EVERYTHING IS PUBLIC
- Every site, and everything it saves, can be read by anyone who has the link. There are no private or password-protected pages. set_visibility only controls whether a site is listed on the person's public page; it is not privacy. Say this plainly if the person asks for privacy, and never put secrets in a page or in saved data.

SAVING DATA FROM A PAGE (forms, RSVPs, votes, guestbooks)
- Two stores per site: one shared JSON "state" document (counters, settings, small lists; atomic ops) and append-only "collections" (one item per submission; newest-first, paged).
- In the page, load the hosted helper and put SH.requireSignIn() before every save:
  <script>window.SH_CONFIG = { site: "<site-name>" };</script>
  <script src="https://simple-host.app/auth.js" defer></script>
  <div id="sh-auth"></div>   then in JS: SH.mount('#sh-auth'); await SH.requireSignIn(); await SH.collection('rsvps').append({...}); await SH.state.patch([{op:'inc', path:'count', by:1}]);
  Reads: const {data} = await SH.state.get(); await SH.collection('rsvps').list({limit:50}).
- Always call SH.requireSignIn() before a save, even where saving works without it today: on the shared address (sites.simple-host.app) saves are moving to require a visitor signed in with Google or an emailed code, and on a site with its own domain (connect_domain) they already do. The same page code works in every case. Saved data stays readable by anyone either way.
- On a failed save keep the form filled, show the error, and never claim success. Never re-send a collection item after an error.
- Pair every form with a page that shows what was collected (e.g. results.html listing the collection), linked quietly from the main page.
- Per-visitor things (drafts, preferences) belong in localStorage, not in shared state.
- You can read and change a site's data directly with get_state, update_state, list_collections, read_collection and add_to_collection.

CARE
- delete_site is permanent: it removes every version and all saved data. Only call it after the person explicitly confirms that specific site.
- A custom domain (connect_domain) needs the person to add one DNS record at their registrar; relay the record exactly and check domain_status.
- If a tool says the connection is no longer signed in, ask the person to reconnect Simple Host in their app's settings.`
