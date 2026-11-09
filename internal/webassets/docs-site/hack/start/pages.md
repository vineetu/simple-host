# Code on your own pages

Load this in the page head, without `defer`:

```html
<script src="https://simple-hack.app/auth.js"></script>
```


1. Create a SQLite resource named `notes`, a `notes` table with `id INTEGER PRIMARY KEY`, `visitor_id TEXT`, and `text TEXT`, and the **personal** preset through the owner API or connector. Follow [Storage and saved data](/docs/guides/storage/).
2. Add `<div id="sh-auth"></div>` to the page and mount sign-in from your page's JavaScript:

   ```js
   await SH.mount('#sh-auth');
   ```

3. On the save button, sign in and add a row:

   ```js
   await SH.requireSignIn();
   const notes = SH.storage.sqlite('notes').table('notes');
   await notes.add({text: 'Bring the projector'});
   ```

4. Read it back and render text with `textContent`:

   ```js
   const result = await notes.list({limit: 20});
   document.querySelector('#notes').textContent =
     result.items.map(row => row.text).join('\n');
   ```


Add a `#notes` element for the result. On a custom domain, set `window.SH_CONFIG = {site:'your-site'}` before loading auth.js. Keep API keys out of page code; use the visitor session for saves.
