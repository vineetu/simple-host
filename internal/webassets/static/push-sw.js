/*
 * Generic Web Push service worker (SIM-360), served at a site's own origin
 * at /v1/push/sw.js (a reserved platform path proxied to Go on every
 * deployment, never a path any site's own files are served under) with
 * an explicit scope of /v1/push/, not root: a push event and a
 * notification click both work from any scope -- neither is about
 * controlling a page -- so this worker never needs, and never takes, the
 * wider scope a site's own root-scoped service worker, if it has one,
 * still exclusively owns (FIX 2/11, Codex round 2). The same path works
 * on every site's own address and on a custom domain alike. Recipes
 * include it with:
 *
 *   <script>navigator.serviceWorker && navigator.serviceWorker.register("/v1/push/sw.js", {scope: "/v1/push/"});</script>
 *
 * SH.push.subscribe() (auth.js) registers it itself, so most pages never
 * need the line above directly. This worker shows the payload Simple Host
 * already built server-side (title/body/url, generic by default: see the
 * owner's push rules) and never reads or decides anything on its own.
 */
// sameOriginURL resolves value against this worker's own origin and
// returns it only when that resolved URL is actually same-origin (and
// http/https); otherwise "/". Checked both when a notification is shown
// (never store a cross-origin target even transiently) and again before
// opening it on click (a stored notification can outlive a domain change,
// e.g. the owner moving or removing a custom domain between the push and
// the click).
function sameOriginURL(value) {
  try {
    var u = new URL(value || "/", self.location.origin);
    if (u.origin === self.location.origin && (u.protocol === "https:" || u.protocol === "http:")) {
      return u.href;
    }
  } catch (e) {}
  return self.location.origin + "/";
}

self.addEventListener("push", function (event) {
  var data = {};
  try { data = event.data ? event.data.json() : {}; } catch (e) {}
  var title = data.title || "New activity";
  var options = {
    body: data.body || "Something was added or changed.",
    tag: data.tag || "simple-host",
    data: {url: sameOriginURL(data.url)}
  };
  event.waitUntil(self.registration.showNotification(title, options));
});

self.addEventListener("notificationclick", function (event) {
  event.notification.close();
  var url = sameOriginURL(event.notification.data && event.notification.data.url);
  event.waitUntil(
    self.clients.matchAll({type: "window", includeUncontrolled: true}).then(function (clients) {
      for (var i = 0; i < clients.length; i++) {
        if (clients[i].url === url && "focus" in clients[i]) return clients[i].focus();
      }
      if (self.clients.openWindow) return self.clients.openWindow(url);
    })
  );
});
