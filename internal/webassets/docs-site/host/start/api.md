# Use the REST API

1. Get a key and keep it in your local environment as `SH_KEY`.

   Open your sites page, **Settings**, then **Keys**. Name the key, press **Create key**, and copy it when shown.

2. Read your account from a terminal:

   ```sh
   curl -H "X-API-Key: $SH_KEY" https://simple-host.app/v1/me
   ```

3. Publish one file.

   ```sh
   curl -X POST https://simple-host.app/v1/sites/hello/files \
     -H "X-API-Key: $SH_KEY" -H 'Content-Type: application/json' \
     --data '{"files":{"index.html":"<!doctype html><h1>Hello</h1>"}}'
   ```

4. Read `code` in an error body shaped as `{"error":"...","code":"..."}`. Branch on the code, and display `error` to the person. Honor `Retry-After` before retrying a limited request.


- [API reference](/docs/api/)
