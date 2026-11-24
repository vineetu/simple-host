// Browser push for the owner's dashboard: shows what the service sent
// ({title, body, url}) and opens that address on a click, only on this origin.
function sameOrigin(value) {
  try {
    var u = new URL(value || "/dashboard", self.location.origin);
    if (u.origin === self.location.origin) return u.href;
  } catch (e) {}
  return self.location.origin + "/dashboard";
}
self.addEventListener("push", function (event) {
  var data = {};
  try { data = event.data ? event.data.json() : {}; } catch (e) {}
  event.waitUntil(self.registration.showNotification(data.title || "Simple Host", {
    body: data.body || "Something changed on one of your sites.",
    tag: data.tag || "simple-host",
    data: {url: sameOrigin(data.url)}
  }));
});
self.addEventListener("notificationclick", function (event) {
  event.notification.close();
  event.waitUntil(self.clients.openWindow(sameOrigin(event.notification.data && event.notification.data.url)));
});
