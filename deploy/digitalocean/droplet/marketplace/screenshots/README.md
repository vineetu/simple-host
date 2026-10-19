# Screenshots to capture

Captured 2026-10-19 on a throwaway `s-1vcpu-1gb` droplet made from the
snapshot `simple-host-24-04-snapshot-1792383026` (v0.7.19), with the test
domain `onebox-demo.simple-host.site`. All eight are 1280 x 800 PNG, light
theme. The admin key, account keys and the SSH client's address are blurred.
The terminal shots are the real session output, set in a terminal frame. To
retake them, use a real droplet made from the image and a test domain (not a
customer's).

1. `01-first-login.png`: the terminal at first SSH login, showing the setup
   heading, the droplet IP and the two addresses answered.
2. `02-dns-check.png`: the terminal at the DNS step, with the one-line records
   instruction and every name `ok`.
3. `03-install-done.png`: the terminal after the install, showing
   "Simple Host is running." and the dashboard URL (admin key blurred).
4. `04-admin-overview.png`: `https://<domain>/admin`, the Overview tab after
   signing in with the admin key.
5. `05-admin-accounts.png`: the admin page issuing accounts for the people who publish.
6. `06-site-live.png`: a published site open in the browser at its HTTPS
   address, with a visitor filling its form.
7. `07-saved-data.png`: the entries a visitor saved, read by the owner. The
   site is a catering page with a quote request form (inbox preset) on its
   own address; the dashboard sets who may read and add, and the owner reads
   the entries through the API (or their AI app).
8. `08-motd.png`: the login message on a later login, showing the dashboard
   URL and the admin key command.

The Marketplace form also takes a square logo (SVG or PNG, at least 400 × 400):
take it from simple-host.app's own mark, not a screenshot.
