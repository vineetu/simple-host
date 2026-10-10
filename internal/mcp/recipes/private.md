# Private: data only the owner sees (internal notes, inventory, drafts)

Use this for data the site's pages never show to visitors: internal notes, stock counts, costings, drafts before they go public. private is the default preset for a new resource. Visitors get 403 for every action.

Two ways the owner works with it:
- Through these tools (storage_get_kv, storage_put_kv, storage_sql_query, storage_sql_execute). This always works.
- From a page on the site itself, signed in with their account email (who_am_i shows it; tell the person that address) on the site's own address: the page acts with owner rights for saved data (SH.me() answers site_owner: true). Use preset custom with all four actions nobody instead of private when the data must never be reachable from any page, not even the owner's.

## 1. Owner setup

storage_set_resource {"site": "{site}", "name": "notes", "body": {"kind": "kv", "preset": "private", "site_passcode": "inherit"}}

## 2. An owner-only page (notes.html), optional

<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex">
<title>Notes</title>
<style>
  body { font: 16px/1.5 system-ui, sans-serif; margin: 0 auto; max-width: 640px; padding: 16px; }
  textarea { font: inherit; width: 100%; min-height: 12em; } button { font: inherit; padding: 4px 10px; }
</style>
<script>window.SH_CONFIG = { site: "{site}" };</script>
<script src="https://simple-host.app/auth.js" defer></script>
</head>
<body>
<h1>Notes</h1>
<p id="who" role="status"></p>
<section id="sh-auth"></section>
<div id="editor" hidden>
  <textarea id="text"></textarea>
  <p><button id="save">Save</button> <span id="saved"></span></p>
</div>
<script>
var notes;
function start() {
  var who = document.getElementById("who"), editor = document.getElementById("editor");
  SH.me().then(function (me) {
    editor.hidden = !me.site_owner;
    if (!me.signed_in) { who.textContent = "Sign in with the site owner's account email."; return; }
    if (!me.site_owner) { who.textContent = "This page is for the site's owner."; return; }
    who.textContent = "Signed in as " + me.email + " (owner).";
    return notes.get("main").then(function (item) { document.getElementById("text").value = item.value; })
      .catch(function (e) { if (e.status !== 404) who.textContent = e.message; });
  });
}
document.addEventListener("DOMContentLoaded", function () {
  notes = SH.storage.kv("notes");
  document.getElementById("save").onclick = function () {
    var saved = document.getElementById("saved");
    saved.textContent = "saving...";
    notes.set("main", document.getElementById("text").value).then(function () { saved.textContent = "saved"; })
      .catch(function (e) { saved.textContent = "not saved: " + e.message; });
  };
  SH.mount("#sh-auth").then(start);
  window.addEventListener("sh:signed-in", start);
});
</script>
</body>
</html>

The trade-off, to tell the person: while they are signed in on their own site, a bug or a malicious script on that site's pages could read or change this data with their rights. Keep such a page free of visitors' content and third-party scripts.

## 3. Verify

Signed out or signed in as anyone else, the page shows no editor and SH.storage.kv("notes").get("main") answers 403. Signed in as the owner's account email, the note loads and saves; storage_get_kv {"site": "{site}", "name": "notes", "key": "main"} shows the same text.
