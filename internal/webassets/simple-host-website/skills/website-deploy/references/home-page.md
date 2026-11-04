# Your home page, showcase and bio

For "make this my home page", use the signed-in connector's `set_home_page`
with `{"site":"portfolio"}`, or a preconfigured owner credential with
`PUT /v1/me/home` and that body. `{"site":null}` restores the showcase.
`who_am_i` reports `home_site`. The person address serves the chosen site's files,
sign-in and storage; its normal site address keeps working. Use `https://simple-host.app/auth.js`, with
`window.SH_CONFIG={site:'portfolio'}` if the page sets an explicit site.
If the home is offline or taken down the showcase appears; rename follows the
site and deleting it clears the choice. The dashboard's Your address panel also
sets it. Available on Simple Host with person addresses, outside hosted events.
"Show my projects on my home page" can mean keeping the default public showcase
or building a site and making it home; unlisted sites stay off the showcase.

For "build me a home page that shows my projects", fetch the public live feed,
render with `textContent`, then choose that site as home. For example:

```html
<ul id="projects"></ul>
<script>
fetch('https://simple-host.app/v1/u/YOUR_HANDLE/showcase.json', {credentials:'omit'})
  .then(r => r.json()).then(feed => feed.sites.forEach(site => {
    const li = document.createElement('li'), a = document.createElement('a');
    a.textContent = site.title || site.name; a.href = site.url;
    li.append(a); document.querySelector('#projects').append(li);
  }));
</script>
```

Substitute the signed-in person's handle. The feed includes only the public
showcase's entries (never unlisted, offline, passcode or taken-down sites), plus
`bio`, `updated_at`, `pinned` and `order`. It allows cross-origin reads without
credentials, caches for 30 seconds and uses the public read rate limits.

To curate the default showcase, use connector `set_bio` with `{"bio":"I make things"}`
(empty clears), and `set_showcase_site` with `{"site":"project","pinned":true,"order":10}`.
Owner REST clients use `GET/PUT /v1/me/bio` and `GET/PUT /v1/sites/{name}/showcase`.
Both refuse deploy-only keys. Pin/order may be changed separately; order is an
integer from 0 to 1000000. Pinned projects come first, then smaller order numbers,
then creation order for ties. Unlisted sites stay hidden even when pinned.
The plain-text bio's limit is returned as `max_length`; it defaults to 280 characters
and is configured by `SHOWCASE_BIO_MAX_LENGTH`. Render it with `textContent`.
The dashboard's Your showcase has the same controls. Hosted events are excluded.

On a small-box install, these home/showcase operations also work with the default
path addresses. Use the configured API origin for the feed and owner calls;
`sites.<domain>/<handle>` redirects to the selected site’s normal URL, while the owner
dashboard stays at `<domain>/<handle>`. Use URLs returned by the instance.
Whole-space custom domains have not shipped. Simple Hack has no personal sites.
