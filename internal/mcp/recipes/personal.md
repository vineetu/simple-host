# Personal data: a wishlist, saved settings, notes, a profile

Use this when each signed-in person keeps their own data that only they see and change, and that follows them to any device: a wishlist, bookmarks, a habit log, notes, a profile, a cart across devices. If others should see it too (a wishlist friends can view), use topic wall instead.

Who signs in where:
- Each person signs in on the site's own address with visitor sign-in (the SH.mount box). Nothing is shown until they do.
- You (the owner's connector) can read everything with the storage_* tools, but this is personal data: look only when the person asks, and never publish it.

## 1. Owner setup (run these tools before publishing the page)

Preset personal (read own, add signed-in, edit own, delete own): each person reads, changes, and deletes only what they added.

storage_set_resource {"site": "{site}", "name": "wishlist", "body": {"kind": "sqlite", "preset": "personal", "site_passcode": "inherit"}}

storage_sql_schema {"site": "{site}", "name": "wishlist", "sql": "CREATE TABLE IF NOT EXISTS items (id INTEGER PRIMARY KEY, title TEXT NOT NULL CHECK (length(title) <= 200), link TEXT, got INTEGER NOT NULL DEFAULT 0, created_at TEXT, updated_at TEXT)"}

What the server guarantees: every statement for a visitor carries "visitor_id = this person", so another person's row answers 404 as if it did not exist; id, visitor_id, created_at, and updated_at are set by the server and cannot be edited.

## 2. The page (wishlist.html)

<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>My wishlist</title>
<style>
  body { font: 16px/1.5 system-ui, sans-serif; margin: 0 auto; max-width: 640px; padding: 16px; }
  input, button { font: inherit; padding: 4px 8px; } li { margin: 6px 0; } .got { text-decoration: line-through; color: #666; } .error { color: #b00020; }
</style>
<script>window.SH_CONFIG = { site: "{site}" };</script>
<script src="https://simple-host.app/auth.js" defer></script>
</head>
<body>
<h1>My wishlist</h1>
<section id="sh-auth"></section>
<form id="add" hidden>
  <input name="title" placeholder="What would you like?" maxlength="200" required>
  <input name="link" type="url" placeholder="Link (optional)">
  <button type="submit">Add</button> <span id="status" class="error"></span>
</form>
<ul id="items"></ul>
<script>
function el(tag, text) { var n = document.createElement(tag); if (text != null) n.textContent = text; return n; }
var items;
function show() {
  var list = document.getElementById("items"), form = document.getElementById("add");
  return SH.me().then(function (me) {
    form.hidden = !me.signed_in;
    if (!me.signed_in) { list.textContent = ""; list.appendChild(el("li", "Sign in to see your wishlist.")); return; }
    // Only this person's rows come back.
    return items.list({ order: "id", limit: 200 }).then(function (result) {
      list.textContent = "";
      SH.storage.toObjects(result).forEach(function (it) {
        var li = el("li"), box = el("input"), label = el("span", " " + it.title + " ");
        box.type = "checkbox"; box.checked = !!it.got; label.className = it.got ? "got" : "";
        box.onchange = function () { items.edit(it.id, { got: box.checked ? 1 : 0 }).then(show).catch(function (e) { label.textContent = " " + e.message; }); };
        li.appendChild(box); li.appendChild(label);
        if (it.link && /^https?:\/\//.test(it.link)) { var a = el("a", "link"); a.href = it.link; a.rel = "noopener"; li.appendChild(a); }
        var del = el("button", "Remove");
        del.onclick = function () { items.delete(it.id).then(show).catch(function (e) { label.textContent = " " + e.message; }); };
        li.appendChild(el("span", " ")); li.appendChild(del);
        list.appendChild(li);
      });
      if (!result.rows.length) list.appendChild(el("li", "Nothing yet."));
    });
  }).catch(function (e) { list.textContent = "Could not load: " + e.message; });
}
document.addEventListener("DOMContentLoaded", function () {
  items = SH.storage.sqlite("wishlist").table("items");
  var form = document.getElementById("add"), status = document.getElementById("status");
  form.onsubmit = function (event) {
    event.preventDefault(); status.textContent = "";
    items.add({ title: form.title.value, link: form.link.value || null }).then(function () { form.reset(); return show(); })
      .catch(function (e) { status.textContent = "Not added: " + e.message; });
  };
  SH.mount("#sh-auth").then(show);
  window.addEventListener("sh:signed-in", show);
});
</script>
</body>
</html>

## 3. The owner's view

storage_sql_query {"site": "{site}", "name": "wishlist", "sql": "SELECT count(*) AS items, count(DISTINCT visitor_id) AS people FROM items", "params": []} gives totals without reading anyone's list. Read rows only when the person asks, and never put them in a page.

## 4. Verify before handing over

Sign in as one email, add two items, tick one, remove one. In a private window sign in as another email: the list is empty, and their own items never show the first person's.
