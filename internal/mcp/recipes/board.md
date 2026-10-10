# Shared board: a potluck or sign-up sheet, a team list, a shared shopping list

Use this when everyone signed in sees the whole list, adds to it, and changes any entry (swapping who brings what, ticking off a task), and only the owner removes entries. If each person should remove only their own entry and nobody edits others', use topic wall instead.

Who signs in where:
- Everyone signs in on the site's own address with visitor sign-in (the SH.mount box) before they see or change the list; strangers see nothing.
- You (the owner's connector) read and tidy the list with the storage_* tools. On the site itself, the owner signed in with their account email (who_am_i shows it; tell the person that address) can also delete entries (SH.me() answers site_owner: true).

## 1. Owner setup (run these tools before publishing the page)

Preset board (read signed-in, add signed-in, edit signed-in, delete owner).

storage_set_resource {"site": "{site}", "name": "potluck", "body": {"kind": "sqlite", "preset": "board", "site_passcode": "inherit"}}

storage_sql_schema {"site": "{site}", "name": "potluck", "sql": "CREATE TABLE IF NOT EXISTS dishes (id INTEGER PRIMARY KEY, dish TEXT NOT NULL CHECK (length(dish) <= 120), who TEXT NOT NULL CHECK (length(who) <= 80), created_at TEXT, updated_at TEXT)"}

What the server guarantees: only signed-in people read or write; an edit keeps who first added the row (visitor_id, set by the server) and stamps updated_at; only the owner deletes.

## 2. The page (potluck.html)

<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Potluck</title>
<style>
  body { font: 16px/1.5 system-ui, sans-serif; margin: 0 auto; max-width: 640px; padding: 16px; }
  input, button { font: inherit; padding: 4px 8px; } td { padding: 4px 8px; border-bottom: 1px solid #ddd; } .error { color: #b00020; }
</style>
<script>window.SH_CONFIG = { site: "{site}" };</script>
<script src="https://simple-host.app/auth.js" defer></script>
</head>
<body>
<h1>Potluck sign-up</h1>
<section id="sh-auth"></section>
<form id="add" hidden>
  <input name="dish" placeholder="Dish" maxlength="120" required>
  <input name="who" placeholder="Your name" maxlength="80" required>
  <button type="submit">Add</button> <span id="status" class="error"></span>
</form>
<table id="dishes"></table>
<script>
function el(tag, text) { var n = document.createElement(tag); if (text != null) n.textContent = text; return n; }
var dishes, owner = false;
function show() {
  var table = document.getElementById("dishes"), form = document.getElementById("add");
  return SH.me().then(function (me) {
    form.hidden = !me.signed_in; owner = !!me.site_owner;
    table.textContent = "";
    if (!me.signed_in) { table.appendChild(el("caption", "Sign in to see and join the list.")); return; }
    return dishes.list({ order: "id", limit: 200 }).then(function (result) {
      SH.storage.toObjects(result).forEach(function (d) {
        var tr = el("tr"), who = el("input");
        tr.appendChild(el("td", d.dish));
        who.value = d.who; who.setAttribute("aria-label", "Who brings " + d.dish);
        // Anyone signed in may change who brings it (a swap).
        who.onchange = function () { dishes.edit(d.id, { who: who.value }).catch(function (e) { who.value = d.who; alert(e.message); }); };
        var cell = el("td"); cell.appendChild(who); tr.appendChild(cell);
        if (owner) {
          var del = el("button", "Remove");
          del.onclick = function () { dishes.delete(d.id).then(show).catch(function (e) { alert(e.message); }); };
          var c2 = el("td"); c2.appendChild(del); tr.appendChild(c2);
        }
        table.appendChild(tr);
      });
      if (!result.rows.length) table.appendChild(el("caption", "Nothing on the list yet."));
    });
  }).catch(function (e) { document.getElementById("dishes").textContent = "Could not load: " + e.message; });
}
document.addEventListener("DOMContentLoaded", function () {
  dishes = SH.storage.sqlite("potluck").table("dishes");
  var form = document.getElementById("add"), status = document.getElementById("status");
  form.onsubmit = function (event) {
    event.preventDefault(); status.textContent = "";
    dishes.add({ dish: form.dish.value, who: form.who.value }).then(function () { form.reset(); return show(); })
      .catch(function (e) { status.textContent = "Not added: " + e.message; });
  };
  SH.mount("#sh-auth").then(show);
  window.addEventListener("sh:signed-in", show);
});
</script>
</body>
</html>

## 3. The owner's view

storage_sql_query {"site": "{site}", "name": "potluck", "sql": "SELECT id, dish, who, updated_at FROM dishes ORDER BY id", "params": []}. Remove an entry after the person asks: storage_sql_execute {"site": "{site}", "name": "potluck", "sql": "DELETE FROM dishes WHERE id = ?", "params": [3]}.

## 4. Verify before handing over

Sign in as two people in two windows: both see the same list, each can add and change who brings what, neither sees a Remove button. Signed in as the owner's account email on the site, Remove appears and works. Signed out, the list is hidden.
