# Public content: a menu, prices, opening hours, a catalogue the owner keeps

Use this for anything the owner writes and everyone reads, and that changes without republishing the site: today's menu, prices, opening hours, an event schedule, FAQs, a product catalogue, a gallery the owner fills (files, same preset).

Who signs in where:
- Readers need no sign-in.
- You (the owner's connector) write the content with storage_put_kv, storage_sql_execute, or storage_put_file. The owner signed in on the site with their account email can also change it from a page (SH.me() answers site_owner: true).

## 1. Owner setup (run these tools before publishing the page)

Preset public (read anyone, add owner, edit owner, delete owner).

storage_set_resource {"site": "{site}", "name": "menu", "body": {"kind": "kv", "preset": "public", "site_passcode": "inherit"}}

storage_put_kv {"site": "{site}", "name": "menu", "key": "today", "value": {"updated": "2026-10-10", "hours": "8:00 to 15:00", "dishes": [{"name": "Idli", "price_cents": 450}, {"name": "Masala dosa", "price_cents": 750}]}}

To change the menu later, call storage_put_kv again with the new value; the page shows it on the next load. For a catalogue with many rows, use a SQLite resource with the same preset and a table (storage_sql_schema, then storage_sql_execute INSERTs); the page reads it with SH.storage.sqlite("catalog").table("products").list(). For photos, a files resource with the same preset and storage_put_file (under 1,000,000 bytes each; resize first).

## 2. The page (index.html or a section of it)

<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Today's menu</title>
<style>
  body { font: 16px/1.5 system-ui, sans-serif; margin: 0 auto; max-width: 560px; padding: 16px; }
  li { display: flex; justify-content: space-between; border-bottom: 1px dotted #ccc; padding: 4px 0; }
</style>
<script>window.SH_CONFIG = { site: "{site}" };</script>
<script src="https://simple-host.app/auth.js" defer></script>
</head>
<body>
<h1>Today's menu</h1>
<p id="hours"></p>
<ul id="menu"><li>Loading...</li></ul>
<script>
function el(tag, text) { var n = document.createElement(tag); if (text != null) n.textContent = text; return n; }
document.addEventListener("DOMContentLoaded", function () {
  var list = document.getElementById("menu");
  // get() resolves to {key, value}; read .value. No sign-in needed for a public resource.
  SH.storage.kv("menu").get("today").then(function (item) {
    var menu = item.value;
    document.getElementById("hours").textContent = "Open " + menu.hours;
    list.textContent = "";
    menu.dishes.forEach(function (d) {
      var li = el("li"); li.appendChild(el("span", d.name)); li.appendChild(el("span", "$" + (d.price_cents / 100).toFixed(2)));
      list.appendChild(li);
    });
  }).catch(function (e) {
    list.textContent = "";
    list.appendChild(el("li", e.status === 404 ? "The menu is not up yet." : "Could not load the menu: " + e.message));
  });
});
</script>
</body>
</html>

## 3. Verify before handing over

Open the page signed out and see the menu. Change one price with storage_put_kv, reload, and see it change. A visitor's attempt to write (SH.storage.kv("menu").set(...)) answers 403.
