# Run and operate

## Open the admin console
[What this is](/features.html#admin-console)


1. Open `/admin` and sign in with an admin account.
2. Read the account, site, and usage lists before changing anything.
3. For a client, request `GET /v1/admin/usage` with the admin key and inspect `workers`.


## Take down a site
[What this is](/features.html#admin-takedown)


1. Read `GET /v1/admin/sites` and identify the site's ID.
2. Confirm the site, then send `POST /v1/admin/sites/{id}/suspend`.
3. To undo the takedown, send `POST /v1/admin/sites/{id}/restore`.


## Check server health
[What this is](/features.html#admin-health)


1. Request `GET /healthz` and check for HTTP 200.
2. If it fails, read the server's startup logs and check database connectivity.
3. On your own server, run `simple-host migrate -status` to check pending migrations.


## Prepare an installation
[What this is](/features.html#admin-setup-helper)


1. Open [Setup](/setup) and choose the installation type.
2. Fill in the server addresses and settings.
3. Download the generated configuration and follow its commands on your own server.


## Run your own copy
[What this is](/features.html#self-host)


1. Follow the [installation instructions](https://github.com/vineetu/simple-host#self-hosting) for your server.
2. Set this install's address and apply its database schema or migrations before starting it.
3. Open `/healthz`, sign in, and publish a test site. Use the [settings reference](https://github.com/vineetu/simple-host/tree/main/docs/advanced) when changing limits or providers.
