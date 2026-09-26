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
   {"email": "<user@example.com>", "code": "<6-digit code>"}
   ```

   Success returns `api_key`, `username`, `handle`, `id`, and `is_admin`. The
   `handle` is the person's part of every site address
   (`https://<sitename>.<handle>.simple-host.app/`).

5. **Save** `api_key`, `username`, and `handle` to the config file. The key
   cannot be shown again, so this file is the source of truth from here on. It
   keeps working until the user rotates keys; signing in again issues another
   key without retiring this one. Re-read `handle` any time via `GET /v1/me`.

   Never print the key into the transcript, a log, or a committed file.

## Failure modes

| Response | Meaning | Do this |
|---|---|---|
| `401 invalid or expired code` | Wrong code, or older than 15 minutes | Try once more, else restart from step 2 |
| `401 too many attempts` | Three wrong codes burned the token | Restart from step 2 |
| `500 could not send verification email` | The server's mail gateway is misconfigured | Tell the user plainly; do not retry blindly |
