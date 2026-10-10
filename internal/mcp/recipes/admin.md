# Owner admin page: see every order and set its status, on the site itself

Use this when the person wants to manage what visitors saved from a page of their own site: all orders with a status to set, every booking, every entry. It pairs with the records recipe (topic records): the same "orders" resource and table. For another resource, change the names.

How it works:
- The owner opens the admin page on the site's own address (its <site>.<handle>.simple-host.app or its own domain) and signs in with the same sign-in box visitors use, with the email of their Simple Host account. There, and only there, the page acts with owner rights for saved data: it reads every row and changes them as the preset's "owner" value allows. SH.me() then answers site_owner: true.
- It is never more than saved data: the page cannot change settings, deploys, domains, passcodes, named viewers, keys, versions, or delete the site. Those stay with these tools.
- Anyone else who opens the page sees only what the preset gives them (with records, their own orders), so the page needs no secret and no key. Still, keep it off the site's menus.
- The trade-off, to tell the person: while they are signed in on their own site, a bug or a malicious script on that site's pages could act on that site's data with their rights. So the page below writes every visitor's text with textContent, never innerHTML, and loads no third-party scripts.

## 1. Owner setup

Nothing new: the records recipe's resource is already right. Its preset (records: read own, add signed-in, edit owner, delete owner) lets the owner on the site read every order and edit the status. Check with storage_list_resources {"site": "{site}"}: orders shows preset records.

## 2. The page (admin.html)

<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex">
<title>Orders admin</title>
<style>
  body { font: 16px/1.5 system-ui, sans-serif; margin: 0 auto; max-width: 900px; padding: 16px; }
  table { border-collapse: collapse; width: 100%; } td, th { text-align: left; padding: 4px 8px; border-bottom: 1px solid #ddd; vertical-align: top; }
  select, button { font: inherit; } .error { color: #b00020; }
</style>
<script>window.SH_CONFIG = { site: "{site}" };</script>
<script src="https://simple-host.app/auth.js" defer></script>
</head>
<body>
<h1>Orders</h1>
<p id="who" role="status"></p>
<section id="sh-auth"></section>
<label>Show <select id="filter"><option value="">all</option><option>received</option><option>preparing</option><option>shipped</option><option>cancelled</option></select></label>
<div id="orders"></div>
<p><button id="more" hidden>Load more</button></p>
<script>
var STATUSES = ["received", "preparing", "shipped", "cancelled"];
function el(tag, text) { var n = document.createElement(tag); if (text != null) n.textContent = text; return n; }
function money(c) { return "$" + (Number(c || 0) / 100).toFixed(2); }
var orders, next = "";

function row(o) {
  var tr = el("tr");
  tr.appendChild(el("td", "#" + o.id));
  var items = []; try { items = JSON.parse(o.items); } catch (e) {}
  tr.appendChild(el("td", items.map(function (l) { return l.qty + " x " + l.name; }).join(", ")));
  tr.appendChild(el("td", money(o.total_cents)));
  tr.appendChild(el("td", o.note || ""));
  tr.appendChild(el("td", (o.created_at || "").slice(0, 16).replace("T", " ")));
  var cell = el("td"), pick = el("select"), msg = el("span");
  STATUSES.forEach(function (s) { var opt = el("option", s); opt.selected = s === o.status; pick.appendChild(opt); });
  // edit() is a PATCH of one row; the server allows it because the owner is signed in on the site.
  pick.onchange = function () {
    msg.className = ""; msg.textContent = " saving...";
    orders.edit(o.id, { status: pick.value }).then(function () { msg.textContent = " saved"; })
      .catch(function (e) { msg.className = "error"; msg.textContent = " not saved: " + e.message; pick.value = o.status; });
  };
  cell.appendChild(pick); cell.appendChild(msg); tr.appendChild(cell);
  return tr;
}

function load(more) {
  var box = document.getElementById("orders"), status = document.getElementById("filter").value;
  var options = { order: "id", desc: 1, limit: 50 };
  if (status) options.where = { status: status };
  if (more) options.after = next;
  return orders.list(options).then(function (result) {
    var table = box.querySelector("table");
    if (!more || !table) {
      box.textContent = "";
      table = el("table");
      var head = el("tr");
      ["Order", "Items", "Total", "Note", "Placed", "Status"].forEach(function (h) { head.appendChild(el("th", h)); });
      table.appendChild(head); box.appendChild(table);
    }
    SH.storage.toObjects(result).forEach(function (o) { table.appendChild(row(o)); });
    next = result.next_after;
    document.getElementById("more").hidden = !next;
    if (!result.rows.length && !more) box.textContent = "No orders.";
  }).catch(function (e) { box.textContent = "Could not load orders: " + e.message; });
}

function start() {
  var who = document.getElementById("who");
  SH.me().then(function (me) {
    if (!me.signed_in) { who.textContent = "Sign in with your Simple Host account email to manage orders."; return; }
    // Anyone else only ever sees what the preset gives them; say so instead of showing an empty table.
    if (!me.site_owner) { who.textContent = "This page is for the shop's owner."; document.getElementById("orders").textContent = ""; return; }
    who.textContent = "Signed in as " + me.email + " (owner).";
    return load(false);
  });
}

document.addEventListener("DOMContentLoaded", function () {
  orders = SH.storage.sqlite("orders").table("orders");
  document.getElementById("filter").onchange = function () { load(false); };
  document.getElementById("more").onclick = function () { load(true); };
  SH.mount("#sh-auth").then(start);
  window.addEventListener("sh:signed-in", start);
});
</script>
</body>
</html>

Notes on the page:
- where: {status: "shipped"} filters on one column by equality (up to three columns); order and desc sort; after: next_after pages on.
- A status the preset does not let the caller change answers 403, and the select goes back to the saved value.
- Pages, the owner's admin page included, cannot make a change that cascades into other rows or tables (a foreign key with ON DELETE CASCADE, or a trigger that writes elsewhere); those go through storage_sql_execute.
- Keep auth.js on the page: it tells the server when the page is framed by another site, and a framed page never gets owner rights.
- The owner's account email is the one they sign in to Simple Host with: call who_am_i and tell the person that exact address. Signing in with any other address makes them an ordinary visitor.

## 3. Verify before handing over

1. Publish admin.html with update_site. Open it on the site's own address, sign in with the owner's account email: every order appears, and changing a status saves.
2. In a private window, sign in as a customer: the page says it is for the shop's owner, and My orders on the shop page still shows only that customer's orders, with the new status.
3. storage_sql_query {"site": "{site}", "name": "orders", "sql": "SELECT id, status FROM orders ORDER BY id DESC", "params": []} shows the status the page set.
