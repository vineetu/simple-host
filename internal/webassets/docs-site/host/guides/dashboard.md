# Owner dashboard

## Open your sites
[What this is](/features.html#dashboard-your-sites)


1. Open [the dashboard](/dashboard) and sign in with the account that owns the sites.
2. Follow **Your sites** to your owner page. Open the **Sites** tab.
3. Use the site's **Manage** menu for its domain, versions, offline setting, passcode, or named viewers. Check the site name before confirming a change.
4. Open the site's live link to check the result. Use a signed-out browser to check visitor access.


## Add photos
[What this is](/features.html#dashboard-add-photos)


1. Ask your chat for a photo link, open the site's **Add photos** action, or open [Add photos](/photos).
2. Choose up to 30 photos and edit their names. A chat link covers one site for up to 60 minutes and 10 saves; use the expiry shown on the link.
3. Press **Save to site**. Keep this page open until it confirms the save.
4. Copy the photo addresses or short message back into chat. Ask your AI app to place those `images/` files on the page.
5. Open the live page and check each photo. Use Versions to roll back if needed.


## Change your account settings
[What this is](/features.html#dashboard-main)


1. Open **Settings** on your sites page.
2. Use **Keys** for local clients, **Connected apps** to disconnect an app, and **Notifications** to manage webhook receivers.
3. To update your sign-in email, follow **Change email** and enter the codes on that screen.
4. Before deleting an account, download its data from **Your data**, then read the deletion confirmation.


## Errors you may see

| Code | Means | Do this |
|---|---|---|
| `photo_link_expired` (404) | Photo link expired | Ask for a new photo link |
| `rate_limited` (429) | Too many requests | Wait for Retry-After |
| `not_found` (404) | Photo upload API: site no longer exists | Choose the site again |
| `photo_link_site_gone` (404) | Chat photo link: site no longer exists | Ask for a new photo link |

## Next
- [Sites and deploy](/docs/guides/publish.md)
- [Notifications](/docs/guides/notifications.md)
