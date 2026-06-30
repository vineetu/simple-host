/* =====================================================================
   Service worker — Architecture Doc PWA
   ---------------------------------------------------------------------
   Strategy:
     - Precache the same-origin app shell on install (so the page works
       fully offline once visited).
     - cache-first with network fallback for SAME-ORIGIN GET requests
       (navigations + static assets). On a network miss for a navigation
       we fall back to the cached index.html app shell.
     - CROSS-ORIGIN requests (the comments widget from simple-host.app
       and its state API calls) are passed straight through to the
       network and are NEVER cached. Their freshness/correctness must
       not be intercepted by us.
   Bump CACHE_NAME whenever the shell file list/content changes; old
   caches are deleted on activate.
   ===================================================================== */

const CACHE_NAME = 'arch-v1';

// Same-origin app shell. Keep this list in sync with what the page needs
// to render offline.
const SHELL = [
  './',
  'index.html',
  'manifest.json',
  'icon.svg'
];

// --- install: precache the shell -------------------------------------
self.addEventListener('install', (event) => {
  event.waitUntil(
    caches.open(CACHE_NAME).then((cache) => cache.addAll(SHELL))
  );
  // Activate this worker as soon as it's finished installing.
  self.skipWaiting();
});

// --- activate: drop any old versioned caches -------------------------
self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches.keys().then((keys) =>
      Promise.all(
        keys.filter((k) => k !== CACHE_NAME).map((k) => caches.delete(k))
      )
    ).then(() => self.clients.claim())
  );
});

// --- fetch: cache-first for same-origin, passthrough otherwise -------
self.addEventListener('fetch', (event) => {
  const req = event.request;

  // Only handle GET; let writes (PUT/POST) and others go to the network.
  if (req.method !== 'GET') return;

  const url = new URL(req.url);

  // Cross-origin (comments widget + its state API): do not intercept.
  // Returning without calling respondWith lets the browser handle it.
  if (url.origin !== self.location.origin) return;

  // Same-origin GET: cache-first, fall back to network, then to the
  // cached app shell for navigations.
  event.respondWith(
    caches.match(req).then((cached) => {
      if (cached) return cached;
      return fetch(req)
        .then((res) => {
          // Cache successful basic (same-origin) responses for next time.
          if (res && res.ok && res.type === 'basic') {
            const copy = res.clone();
            caches.open(CACHE_NAME).then((cache) => cache.put(req, copy));
          }
          return res;
        })
        .catch(() => {
          // Offline and not cached: serve the shell for navigations.
          if (req.mode === 'navigate') return caches.match('index.html');
          return Response.error();
        });
    })
  );
});
