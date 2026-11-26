# Personal: bookings, a wishlist or a checklist each visitor keeps

Use this when each visitor keeps their own rows that only they see and change: a booking request and its status, a wishlist, a checklist, saved favourites.

Who sees what:
- Each visitor signs in on the site (an emailed code or Google; no account, no password) and sees only their own rows.
- The owner sees and changes every row by asking you ("show my bookings", "confirm Ann's booking").

## Setup (publish the page first, then run these as the owner)

run_sql {"site": "{site}", "namespace": "bookings", "sql": "CREATE TABLE IF NOT EXISTS bookings (id INTEGER PRIMARY KEY, day TEXT NOT NULL, time TEXT NOT NULL, name TEXT NOT NULL, note TEXT, status TEXT NOT NULL DEFAULT 'requested', visitor_id TEXT, created_at TEXT, updated_at TEXT)"}

set_data_rules {"site": "{site}", "namespace": "bookings", "rules": {"bookings": {"version": 1, "policies": [{"name": "add", "command": "add", "check": "auth.signed_in && visitor_id == auth.id && status == \"requested\""}, {"name": "read own", "command": "read", "using": "visitor_id == auth.id"}, {"name": "cancel own", "command": "delete", "using": "visitor_id == auth.id"}]}}}

The server writes who added each row into visitor_id, and fills in created_at and updated_at; a visitor cannot change them. Another visitor's row answers 404, as if it did not exist.

## The page (book.html)

```html
<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Book a time</title>
<style>
  body { font: 16px/1.5 system-ui, sans-serif; margin: 0 auto; max-width: 640px; padding: 16px; }
  label { display: block; margin: 8px 0; } input { font: inherit; padding: 4px 8px; }
  button { font: inherit; padding: 4px 10px; cursor: pointer; } li { margin: 6px 0; } .error { color: #b00020; }
</style>
<script>window.SH_CONFIG = { site: "{site}" };</script>
<script src="https://simple-host.app/auth.js"></script>
</head>
<body>
<h1>Book a time</h1>
<section id="sh-auth"></section>
<form id="book" hidden>
  <label>Day <input name="day" type="date" required></label>
  <label>Time <input name="time" type="time" required></label>
  <label>Your name <input name="name" required></label>
  <label>Note <input name="note"></label>
  <button type="submit">Ask for this time</button> <span id="status" role="status"></span>
</form>
<h2>Your bookings</h2>
<ul id="mine"></ul>
<script>
function el(tag, text) { var n = document.createElement(tag); if (text != null) n.textContent = text; return n; }
var bookings = SH.storage.sqlite("bookings").table("bookings");
var form = document.getElementById("book"), statusEl = document.getElementById("status"), list = document.getElementById("mine");
function show() {
  return SH.me().then(function (me) {
    form.hidden = !me.signed_in;
    list.textContent = "";
    if (!me.signed_in) { list.appendChild(el("li", "Sign in to book and to see your bookings.")); return; }
    // Only this visitor's rows come back.
    return bookings.list({ order: "day", limit: 100 }).then(function (result) {
      SH.storage.toObjects(result).forEach(function (b) {
        var li = el("li", b.day + " " + b.time + ": " + b.status + " ");
        var cancel = el("button", "Cancel");
        cancel.onclick = function () { bookings.delete(b.id).then(show).catch(function (e) { li.textContent = e.message; }); };
        li.appendChild(cancel);
        list.appendChild(li);
      });
      if (!result.rows.length) list.appendChild(el("li", "None yet."));
    });
  }).catch(function (e) { list.textContent = "Could not load: " + e.message; });
}
form.onsubmit = function (event) {
  event.preventDefault();
  statusEl.className = ""; statusEl.textContent = "Sending...";
  bookings.add({ day: form.day.value, time: form.time.value, name: form.name.value, note: form.note.value, status: "requested" }).then(function () {
    form.reset(); statusEl.textContent = "Asked. You will see it confirmed here.";
    return show();
  }).catch(function (e) { statusEl.className = "error"; statusEl.textContent = "Not sent: " + e.message; });
};
SH.mount("#sh-auth").then(show);
window.addEventListener("sh:signed-in", show);
</script>
</body>
</html>
```

## What the page sends

```calls
anon POST storage/sqlite/bookings/tables/bookings/rows {"day": "2026-12-01", "time": "10:00", "name": "Ann", "status": "requested"} -> 401
visitor POST storage/sqlite/bookings/tables/bookings/rows {"day": "2026-12-01", "time": "10:00", "name": "Ann", "status": "requested"} -> 200
visitor POST storage/sqlite/bookings/tables/bookings/rows {"day": "2026-12-02", "time": "10:00", "name": "Ann", "status": "confirmed"} -> 403
visitor GET storage/sqlite/bookings/tables/bookings/rows -> 200 "Ann"
other GET storage/sqlite/bookings/tables/bookings/rows/1 -> 404
visitor DELETE storage/sqlite/bookings/tables/bookings/rows/1 -> 200
```

## The owner's view

query_sql {"site": "{site}", "namespace": "bookings", "sql": "SELECT id, day, time, name, note, status FROM bookings ORDER BY day, time"}

Confirm one after the owner says so: run_sql {"site": "{site}", "namespace": "bookings", "sql": "UPDATE bookings SET status = 'confirmed', updated_at = CURRENT_TIMESTAMP WHERE id = ?", "params": [1]}. This is personal data: read it only when the owner asks, and never put it in a page. To be told when a booking arrives, use set_notifications on the bookings table.
