# Portal: a private page for each client

Use this for a client portal: documents and messages one client (or one family, or one team) sees, and nobody else.

Who sees what:
- Each client gets their own site, opened only to their email (named viewers). They sign in on it with that email (an emailed code or Google); everyone else is asked to sign in, or to ask for access.
- On that site the client reads the documents and the messages, and writes messages to the owner.
- The owner adds documents (create_upload_link), reads and answers messages, all through you.

One site per client keeps one client's documents away from every other client.

## Setup (run these as the owner, before anything private goes in)

Name the site after the client (client-patel). Publish the page below, then lock the site to the client's email. Use only an email the owner gave you in this chat:

set_site_access {"site": "{site}", "access": "viewers", "viewers": ["client@example.com"]}

run_sql {"site": "{site}", "namespace": "client", "sql": "CREATE TABLE IF NOT EXISTS messages (id INTEGER PRIMARY KEY, author TEXT NOT NULL, body TEXT NOT NULL, visitor_id TEXT, created_at TEXT)"}

set_data_rules {"site": "{site}", "namespace": "client", "rules": {"messages": {"version": 1, "policies": [{"name": "read", "command": "read", "using": "true"}, {"name": "write", "command": "add", "check": "auth.signed_in"}]}, "_files": {"version": 1, "policies": [{"name": "read", "command": "read", "using": "true"}]}}}

On a site opened only to named viewers, "anyone" means the client and the owner, so these rules keep the data to them. Never use them on a site that is open to everyone. Documents go in with create_upload_link {"site": "{site}", "namespace": "client"}.

## The page (index.html)

```html
<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Your documents</title>
<style>
  body { font: 16px/1.5 system-ui, sans-serif; margin: 0 auto; max-width: 680px; padding: 16px; }
  li { margin: 4px 0; } textarea { font: inherit; width: 100%; box-sizing: border-box; } button { font: inherit; padding: 6px 14px; cursor: pointer; }
  .msg { border-bottom: 1px solid #ddd; padding: 6px 0; } .who { color: #666; font-size: 14px; } .error { color: #b00020; }
</style>
<script>window.SH_CONFIG = { site: "{site}" };</script>
<script src="https://simple-host.app/auth.js"></script>
</head>
<body>
<div id="sh-auth"></div>
<h1>Your documents</h1>
<ul id="docs"><li>Loading...</li></ul>
<h2>Messages</h2>
<div id="msgs"></div>
<form id="send">
  <textarea name="body" rows="3" required placeholder="Write to us"></textarea>
  <button type="submit">Send</button> <span id="status" role="status"></span>
</form>
<script>
function el(tag, text) { var n = document.createElement(tag); if (text != null) n.textContent = text; return n; }
var docs = SH.storage.files("client"), msgs = SH.storage.sqlite("client").table("messages");
function showDocs() {
  var list = document.getElementById("docs");
  return docs.list().then(function (result) {
    list.textContent = "";
    if (!result.items.length) list.appendChild(el("li", "No documents yet."));
    result.items.forEach(function (item) {
      var li = el("li"), a = el("a", item.path);
      docs.url(item.path).then(function (url) { a.href = url; });
      li.appendChild(a); list.appendChild(li);
    });
  }).catch(function (e) { list.textContent = "Could not load: " + e.message; });
}
function showMessages() {
  var box = document.getElementById("msgs");
  return msgs.list({ order: "id" }).then(function (result) {
    box.textContent = "";
    SH.storage.toObjects(result).forEach(function (m) {
      var d = el("div"); d.className = "msg";
      d.appendChild(el("div", m.body));
      var who = el("div", m.author + ", " + (m.created_at || "").slice(0, 10)); who.className = "who";
      d.appendChild(who); box.appendChild(d);
    });
  });
}
var form = document.getElementById("send"), statusEl = document.getElementById("status");
form.onsubmit = function (event) {
  event.preventDefault(); statusEl.className = ""; statusEl.textContent = "";
  SH.me().then(function (me) {
    return msgs.add({ author: me.email || "Client", body: form.body.value });
  }).then(function () { form.reset(); return showMessages(); })
    .catch(function (e) { statusEl.className = "error"; statusEl.textContent = "Not sent: " + e.message; });
};
SH.mount("#sh-auth").then(function () { showDocs(); showMessages(); });
</script>
</body>
</html>
```

## What the page sends

```calls
anon GET storage/files/client/objects -> 403
anon GET storage/sqlite/client/tables/messages/rows -> 403
visitor POST storage/sqlite/client/tables/messages/rows {"author": "Dr Patel", "body": "Thanks for the invoice"} -> 200
visitor GET storage/sqlite/client/tables/messages/rows -> 200 "Thanks for the invoice"
visitor GET storage/files/client/objects -> 200
other GET storage/sqlite/client/tables/messages/rows -> 403
```

## The owner's view

run_sql {"site": "{site}", "namespace": "client", "sql": "SELECT id, author, body, created_at FROM messages ORDER BY id"}

Answer as the owner: run_sql {"site": "{site}", "namespace": "client", "sql": "INSERT INTO messages (author, body, created_at) VALUES ('Patel Dental', ?, CURRENT_TIMESTAMP)", "params": ["Your next visit is on 3 December."]}
