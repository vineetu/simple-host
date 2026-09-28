# Register a user and get an API key

Skip this if the Simple Host tools are available in the session.

Registration is a **two-step email verification** flow. The user proves they own
the address before the server hands out an API key. You never open or read the
user's email: they check it themselves and give you the code.

Config file: `~/.website-deploy/config.json`. Resolve `~` to the OS home
directory yourself — `$HOME/.website-deploy/config.json` on macOS/Linux,
`$env:USERPROFILE\.website-deploy\config.json` in PowerShell. Some tool-call
paths do not expand a literal `~`, and `%USERPROFILE%` expands only in `cmd`.

1. **Check the config file first.** If it holds a non-empty `api_key`,
   registration is already done — stop here and use it. Do not re-register a
   returning user.

2. **Ask for the email and post it.** If the environment already knows the user's
   email, repeat it back and let them confirm rather than asking cold.

   ```
   POST /v1/auth
   Content-Type: application/json
   {"email": "<user@example.com>"}
   ```

   Success is `202` with
   `{"message": "Check your email for a sign-in code.", "email": "...", "expires_in_seconds": 900}`.
   The server has now emailed a 6-digit code.

3. **Ask the user for the code.** They open the email themselves and paste the
   6-digit code into the chat; do not try to read their mailbox. Accept `123456`
   or `123-456`; strip non-digits before sending.

4. **Verify:**

   ```
   POST /v1/auth/verify
   Content-Type: application/json
   {"email": "<user@example.com>", "code": "<6-digit code>", "choose_handle": true, "name": "Claude Code on <machine>"}
   ```

   `name` is optional; it labels the key in the person's Keys list (default
   `agent sign-in`).

   Always send `"choose_handle": true`. For an existing account it is ignored
   and the call signs in. If this sign-in would create a new account, nothing
   is created and the code is not used up: the answer is `409`
   `{"code": "choose_handle", "suggested_handle": "<h>", "address": "<h>.simple-host.app"}`.
   Ask the person which address they want, offering `suggested_handle`, and
   tell them: "Choose the address (handle) when signing up; you can change it
   later from the dashboard, not more than once in 30 days." Then verify again
   with the same code plus `"handle": "<their choice>"` (1 to 39 lowercase
   letters, digits or hyphens, not starting or ending with a hyphen). To check
   a name first: `GET /v1/handles/check?handle=<name>` →
   `{"handle", "available", "address"}` (with `code` and `error` when not
   available).

   Success returns `api_key`, `username`, `handle`, `id`, and `is_admin`. The
   `handle` is the person's part of every site address
   (`https://<sitename>.<handle>.simple-host.app/`).

5. **Save** `api_key`, `username`, and `handle` to the config file. The key
   cannot be shown again, so this file is the source of truth from here on. It
   keeps working until the person revokes it from their Keys list or signs out
   everywhere; signing in again issues another key without retiring this one.
   Keys start with `shk_`. Re-read `handle` any time via `GET /v1/me`; the person can change it.

   Never print the key into the transcript, a log, or a committed file.

## Failure modes

| Response | Meaning | Do this |
|---|---|---|
| `401 invalid or expired code` | Wrong code, or older than 15 minutes | Try once more, else restart from step 2 |
| `401 too many attempts` | Three wrong codes burned the token | Restart from step 2 |
| `500 could not send verification email` | The server's mail gateway is misconfigured | Tell the user plainly; do not retry blindly |
| `409 choose_handle` | A new account: the person picks its address | Ask which address they want (offer `suggested_handle`), then verify again with the same code plus `handle` |
| `409 handle_taken` / `409 handle_reserved` / `400 invalid_handle` | That address cannot be used | Ask for another and verify again with the same code; it is not used up |
