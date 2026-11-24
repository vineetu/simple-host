# Public: a menu, prices or opening hours the owner keeps

Use this for anything the owner writes and everyone reads, and that changes without publishing the site again: today's menu, prices, opening hours, a schedule, a product list.

Who sees what:
- Anyone reads it, without signing in.
- Only the owner changes it, by asking you ("the dosa is now $8").

## Setup (run these as the owner before publishing the page)

set_data_rules {"site": "{site}", "namespace": "menu", "rules": {"_values": {"version": 1, "policies": [{"name": "read", "command": "read", "using": "true"}]}}}

run_sql {"site": "{site}", "namespace": "menu", "sql": "INSERT INTO _values (key, value) VALUES ('today', ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP", "params": ["{\"hours\": \"8:00 to 15:00\", \"dishes\": [{\"name\": \"Idli\", \"price_cents\": 450}, {\"name\": \"Masala dosa\", \"price_cents\": 750}]}"]}

Named values live in the namespace's _values table, each value as JSON text. To change the menu, run the same statement with the new value; the page shows it on the next load. For many rows (a catalogue), make a table instead and give it the same read rule; the page reads it with SH.storage.sqlite("menu").table("products").list(). For the owner's own photos, use create_upload_link; never build an upload form for the owner.

## The page (index.html)

```html
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
<script src="https://simple-host.app/auth.js"></script>
</head>
<body>
<h1>Today's menu</h1>
<p id="hours"></p>
<ul id="menu"><li>Loading...</li></ul>
<script>
function el(tag, text) { var n = document.createElement(tag); if (text != null) n.textContent = text; return n; }
var list = document.getElementById("menu");
// get() answers {key, value}.
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
</script>
</body>
</html>
```

## What the page sends

```calls
anon GET storage/kv/menu/keys/today -> 200 "Masala dosa"
anon PUT storage/kv/menu/keys/today {"value": {"hours": "never"}} -> 401
visitor PUT storage/kv/menu/keys/today {"value": {"hours": "never"}} -> 403
```
