# Site storage resources: KV, SQLite and files

Use these resources for new Simple Host sites that need saved data. Keep each
application's keys, tables and paths in the site's own design; there is no
predefined RSVP, shop or guestbook schema. A resource name is unique across the
three kinds, and its kind cannot later change. The owner creates and configures
it with a Simple Host connector or an owner API key on `https://simple-host.app`.
Pages call the same REST paths **on their own site origin** so the visitor's
site-scoped sign-in and passcode cookie apply. These resources are not available
to Simple Hack team sites or the Enterprise replicated/S3 product.

Declare a resource with `storage_set_resource(site,name,body)` or
`PUT /v1/sites/{site}/storage/resources/{name}`:

```json
{"kind":"kv","read":"anyone","write":"owner","site_passcode":"inherit"}
```

`kind` is `kv`, `sqlite` or `files`. `read` and `write` are independent:
`anyone`, `signed-in` or `owner`, each defaulting to `owner`. `site_passcode`
is `inherit` by default (a visitor must also unlock an existing site passcode)
or `off` (this resource deliberately bypasses that passcode). An owner key
bypasses the passcode but remains owner-scoped. Anonymous writes work **only**
when the owner explicitly chooses `write=anyone`. A signed-in policy gives
every signed-in visitor access to the resource. It does not grant row-level or
per-person privacy; keep the existing Personal and private Submissions API for
that behavior. Ask the owner before making an existing resource public, opening
anonymous writes, or permanently deleting it.

| Task | Connector | REST suffix after `/v1/sites/{site}/storage` |
|---|---|---|
| List resources and policies | `storage_list_resources(site)` | `GET /resources` (owner) |
| Check storage allowance | `storage_get_usage(site)` | `GET /usage` (owner); returns `used_bytes`, `limit_bytes`, `remaining_bytes` and `breakdown` by KV, SQLite and files |
| Create or change a resource | `storage_set_resource(site,name,body)` | `PUT /resources/{name}` (owner) |
| Permanently delete a resource | `storage_delete_resource(site,name)` | `DELETE /resources/{name}` (owner) |
| List KV entries | `storage_list_kv_keys(site,name,prefix?,after?,limit?)` | `GET /kv/{name}/keys?prefix=&after=&limit=` |
| Read, set, remove a KV value | `storage_get_kv`, `storage_put_kv`, `storage_delete_kv` with `site,name,key` (`value` on put) | `GET`, `PUT`, `DELETE /kv/{name}/keys/{key}`; PUT body `{ "value": <JSON> }` |
| Read-only SQL query | `storage_sql_query(site,name,sql,params?)` | `POST /sqlite/{name}/query` with `{sql,params}` |
| Change SQL rows | `storage_sql_execute(site,name,sql,params?)` | `POST /sqlite/{name}/execute` with `{sql,params}` |
| Change SQL schema | `storage_sql_schema(site,name,sql,params?)` | `POST /sqlite/{name}/schema` (owner) |
| List file objects | `storage_list_file_objects(site,name,prefix?,after?,limit?)` | `GET /files/{name}/objects?prefix=&after=&limit=` |
| Upload or remove a file | `storage_put_file(site,name,path,content_base64,content_type)`, `storage_delete_file(site,name,path)` | `PUT` raw bytes with `Content-Type`, `DELETE /files/{name}/objects/{path...}` |
| Download a file | `storage_file_download_link(site,name,path)` for the owner | Browser or direct REST: `GET /files/{name}/objects/{path...}`; connector mints a scoped ten-minute link rather than returning base64 bytes |

The connector's inline file upload is capped at 1 MiB decoded; use direct REST
for larger files. File storage is durable across website publishes and rollbacks;
it is separate from the versioned website files. Stored raster images may render
inline only after the server validates their bytes; other types download as
attachments. Do not use a file object as a way to serve arbitrary HTML or JS
under the site's origin.

The default allowance is **1,000,000 bytes per website**, pooled across these
three new resource kinds. Check `storage_get_usage` or owner-only `GET /usage`
before large writes. The response separates `kv_bytes`, `sqlite_bytes` (the
main database after checkpoint) and `files_bytes`; SQLite's transient WAL is
excluded. This allowance does not include deployed website files or legacy
saved data, which retain their own limits. A rejected growth write returns
`site_full` without changing the stored value or object.

For a page that lets people upload phone photos, resize and compress the image
in the browser before sending its bytes to the files API. Decode with
`createImageBitmap`, draw to a canvas at a smaller width and height while
preserving aspect ratio, and encode as WebP or JPEG with a suitable quality.
Choose a target well below the site's remaining allowance so more than one
image can fit. Show a preview and the compressed byte size; if it is still too
large, explain that the visitor must choose a smaller image or reduce quality.
Leave PDFs and other binary files untouched unless the application explicitly
defines a conversion. The raw-file API stores the bytes it receives.

For example, a browser page can prepare a selected photo before calling
`SH.storage.files('photos').put(path, preparedPhoto)`. Its 350,000-byte target
leaves room for other data in a 1,000,000-byte site; choose a lower target if
`/usage` shows less space remains. Show the returned file in an image preview
and display its `size` before uploading. If preparation throws, show its message
beside the file input and do not upload the original photo.

```js
async function preparePhoto(file, maxBytes = 350000) {
  if (!(file instanceof Blob) || !file.type.startsWith('image/'))
    throw new TypeError('Choose a photo');
  if (!('createImageBitmap' in window))
    throw new Error('This browser cannot resize photos');
  const image = await createImageBitmap(file);
  try {
    const maxEdge = Math.max(image.width, image.height);
    if (!maxEdge) throw new Error('Photo has no dimensions');
    const firstScale = Math.min(1, 1280 / maxEdge);
    for (let shrink = 1; shrink >= 0.2; shrink *= 0.8) {
      const canvas = document.createElement('canvas');
      canvas.width = Math.max(1, Math.round(image.width * firstScale * shrink));
      canvas.height = Math.max(1, Math.round(image.height * firstScale * shrink));
      const ctx = canvas.getContext('2d');
      if (!ctx) throw new Error('This browser cannot draw photos');
      ctx.drawImage(image, 0, 0, canvas.width, canvas.height);
      for (const quality of [0.82, 0.68, 0.54]) {
        const blob = await new Promise((resolve, reject) => canvas.toBlob(
          b => b ? resolve(b) : reject(new Error('Photo encoding failed')),
          'image/webp', quality));
        if (blob.type !== 'image/webp')
          throw new Error('This browser cannot encode WebP photos');
        if (blob.size <= maxBytes) {
          const name = (file.name || 'photo').replace(/\.[^.]+$/, '') + '.webp';
          return new File([blob], name, {type: 'image/webp'});
        }
      }
    }
    throw new Error('Photo is still too large; choose a smaller image');
  } finally {
    image.close();
  }
}
```

SQLite accepts one statement per call with bound positional `?` parameters.
`/query` is read-only; `/execute` changes rows; `/schema` is owner-only for
schema changes. The server's SQLite authorizer enforces operation and resource
boundaries, including nested statements. The query answer has `columns` and
`rows` (BLOB cells are `{ "base64": "..." }`); execute returns `changes` and
`last_insert_id`. Design tables and
indexes for the actual site; never concatenate visitor text into SQL.

For browser calls, use relative `/v1/sites/{site}/storage/...` URLs on the
site's own address. Use `SH.requireSignIn()` when the chosen policy requires
sign-in and the existing hosted auth helper for the site's session. Visitor
writes send the same-site `Origin` and `X-SH-CSRF: 1`; owner tool calls use their
existing credential. Treat `sign_in_required` (401), `forbidden` or
`site_locked` (403), `resource_not_found` (404), validation errors (400), and
`site_full` (507) as server decisions. Do not retry a write with a broader
policy merely to make it succeed.

Older `/state`, `/collections` and `/data` APIs remain supported for existing
sites. Their atomic state operations, ETags, undo/history, notifications,
per-person privacy and item-level semantics do not automatically migrate to
these whole-resource policies. Do not copy old private data into a new
resource without an owner-directed schema and privacy review.
