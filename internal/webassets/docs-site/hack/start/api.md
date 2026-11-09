# Use the REST API

1. Get a key and keep it in your local environment as `SH_KEY`.

   For an account key, open **Account**, **Danger zone**, then **New key**. Confirm that this replaces your old key and disconnects connected apps. For team publishing, use **Website**, then **Advanced: command line and API key** instead.

2. Read your account from a terminal:

   ```sh
   curl -H "X-API-Key: $SH_KEY" https://simple-hack.app/v1/me
   ```

3. Publish one file.

   With your team's key, replace `TEAM` with the site name shown under Website:
   ```sh
   curl -X PUT 'https://simple-hack.app/v1/sites/TEAM/files?create=1' \
     -H "X-API-Key: $SH_KEY" -H 'Content-Type: application/json' \
     --data '{"files":{"index.html":"<!doctype html><h1>Hello</h1>"}}'
   ```

4. Read `code` in an error body shaped as `{"error":"...","code":"..."}`. Branch on the code, and display `error` to the person. Honor `Retry-After` before retrying a limited request.


- [API reference](/docs/api.md)
