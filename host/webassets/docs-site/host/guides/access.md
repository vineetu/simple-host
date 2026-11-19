# Sign-in and access

## Sign in to your account
[What this is](/features.html#signin-email-code)


1. Open the Simple Host sign-in screen in your browser.
2. Use a sign-in method offered on this screen: choose a provider, enter your email and its code, or fill in **Email or username** and **Password** when shown. Keep codes and passwords in the sign-in screen, outside chat.
3. Check the signed-in account before changing a site's access.


## Create a key for a client
[What this is](/features.html#api-key)


1. Open the key controls.

      **Dashboard**
On your sites page, open Settings, then Keys. Enter a name, select Deploy only for CI if appropriate, and press Create key.   **API**
With an existing account key, send `POST /v1/me/keys` with `{"name":"laptop"}`. Use `GET /v1/me/keys` to list keys, and `DELETE /v1/me/keys/{id}` to revoke one.   
2. Copy the new key when shown and store it locally. Send it in `X-API-Key` from your trusted client, never in a hosted page.


## Sign visitors in on a page
[What this is](/features.html#visitor-signin)


1. Load `https://simple-host.app/auth.js` without `defer`. Add a `#sh-auth` element and call `SH.mount('#sh-auth')`.
2. Call `await SH.requireSignIn()` when the visitor presses a button that needs sign-in.
3. Read `await SH.me()` before showing account details. Call `SH.signOut()` for the sign-out action.
4. On a custom domain, set `window.SH_CONFIG = {site:'your-site'}` before loading the script.


## Choose who can open a site
[What this is](/features.html#access-named-viewers)


1. In the site's Manage menu, choose **Who can open**.
2. For named people, add their exact email addresses. Through the API, use `POST /v1/sites/{sitename}/viewers` with `{"emails":["person@example.com"]}`.
3. For a shared passcode, use the site's Passcode controls in the dashboard. Keep the passcode out of chat.
4. Confirm before removing either restriction. Use named viewers or a passcode, not both; List on my profile only controls your profile page.


To create a new site private from the first request, send `access: "specific"` and
optional `viewers: ["person@example.com"]` alongside `files` in the create call
(`create_site` or `POST /v1/sites/{sitename}/files`). With no viewers, only you can
open it. Never publish open and then restrict it. Archive uploads use
`?access=specific&viewers=person@example.com` (comma-separated or repeated viewers).
Omitting access keeps the default: anyone with the address can open it.

With `PUT ?create=1`, viewers are added only when creating. For an existing site,
`access: "specific"` refuses `409 access_differs` unless it is already specific;
use `set_site_access` / `grant_site_viewer` first. `anyone` does not change an
existing site's access. Plain PUT rejects these options with `400 invalid_request`.
Named viewers require visitor sign-in and an address of the site's own.
As on any private site, a preview link or download link you make later opens for
whoever holds it.

## Share sites with a team
[What this is](/features.html#teams)

A team owns sites the way you do, and everyone in it manages them with their own sign-in. Any member can publish, roll back, take offline, rename and delete the team's sites, invite people and remove members, and delete the team once it owns no sites. People join only by accepting an invitation.


1. Open **Teams** from your sites page, or ask your AI app to `create_team`. The name becomes the address, `https://<site>.<team>.simple-host.app/`: lowercase letters, numbers and hyphens, no dots. It cannot be renamed.
2. Invite people by their email (`add_team_member`, or `POST /v1/teams/{team}/members`). Nothing is emailed, so tell them: signed in with that email, they find the invitation on their Teams page (or ask their AI app) and accept or decline it. The answer is the same whether or not anyone has that email. Invitations lapse after 14 days. A person can be in up to 10 teams.
3. Ask your AI app to work on a site with the team as its owner: every site and storage tool takes `owner`, and REST routes take `?owner=<team>`. To avoid overwriting a colleague, send `expected_version` (REST: `If-Match: "<active version>"`); `412 version_changed` means someone published meanwhile.
4. Remove someone and their access stops on their next request. The last member cannot leave: delete the team's sites, then the team. Deleting your account deletes each team only you belong to, with its sites.


## Errors you may see

| Code | Means | Do this |
|---|---|---|
| `missing_api_key` (401) | Owner API key missing | Send `X-API-Key` |
| `sign_in_required` (401) | Visitor session required | Sign in on the site |
| `account_auth_unavailable` (403) | Account sign-in on a site address | Use the platform sign-in screen |
| `use_custom_domain` (400) | Visitor sign-in needs its own origin | Open the site address |

## Next
- [Storage and saved data](/docs/guides/storage.md)
- [Agents and MCP](/docs/guides/agents.md)

Every open site can be found by search engines, whatever List on my profile says. New sites start off. To keep a site out of search engines, use a passcode or named viewers, take it offline, or ship your own robots.txt / noindex.
