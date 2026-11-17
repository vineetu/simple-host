# Passcodes, viewers and domains

## connect_domain

<div data-tool="connect_domain"></div>

Connect a custom domain or claim a short simple-host.app address (optional: every site already has its own at https://&lt;site&gt;.&lt;handle&gt;.simple-host.app/). Either a free `<name>.simple-host.app` address (e.g. `clay-studio.simple-host.app`): active at once, no DNS step, first come first served. Or the person's own domain (e.g. `rsvp.example.com` or `example.com`): returns the two DNS records they must add at their domain registrar, the address record (dns_record) and a TXT ownership record (ownership_record, to keep in place); relay both exactly, then check with domain_status until it is active. Connecting a domain already bound to another of the person's sites moves it to this site. Once active the site lives only at that address, its old address redirects there, and visitors sign in and save there. Or `*.<their domain>` (e.g. `*.trips.example.com`, no `site` needed): an address family for the whole account, so every site X answers at X.trips.example.com once the wildcard DNS record and the TXT ownership record are seen and the operator's wildcard certificate is set up; relay the records and check with domain_status (domain `*.trips.example.com`). Ask before connecting one: it applies to every site of the account.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| domain | string | yes | The address without https://: a free `<name>.simple-host.app`, the person's own domain or subdomain, e.g. `rsvp.example.com`, or `*.<domain>` for an address family covering every site of the account. |
| site | string |  | The site's name as it appears in its address, e.g. `birthday-rsvp`: lowercase letters, numbers and hyphens. Not used for a `*.<domain>` address family. |

## domain_status

<div data-tool="domain_status"></div>

Check whether a site's custom domain is connected: pending (the DNS records are not seen yet, or its certificate is being issued) or active. Shows the certificate's progress and, while pending, the earlier address the site is still served at. With domain `*.<domain>`, checks the account's address family instead (no site needed).

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| domain | string |  | Optional: `*.<domain>` to check an address family of the account. |
| site | string |  | The site's name as it appears in its address, e.g. `birthday-rsvp`: lowercase letters, numbers and hyphens. Not used with a `*.<domain>` domain. |

## remove_domain

<div data-tool="remove_domain"></div>

Disconnect a site's custom domain or free `<name>.simple-host.app` address. Only call this after the person has explicitly confirmed, in this conversation, that they want this specific address disconnected. Pass the address being removed as `confirm_domain` (domain_status shows it). Afterwards the site is served at its own address again (or, if the removed domain was still pending, at the earlier address it was still using). Links to a removed custom domain stop working; a removed free name keeps redirecting to the site and cannot be claimed by anyone else. With confirm_domain `*.<domain>` it disconnects the account's address family (no site needed): every site stops answering under it and goes back to its own address.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| confirm_domain | string | yes | The address being disconnected, typed out, as confirmation, e.g. `rsvp.example.com` or `*.trips.example.com`. |
| site | string |  | The site's name as it appears in its address, e.g. `birthday-rsvp`: lowercase letters, numbers and hyphens. Not used for a `*.<domain>` address family. |

## get_site_passcode

<div data-tool="get_site_passcode"></div>

Show whether a site has a passcode and when it was set. Never shows the passcode in chat; opens the passcode screen in apps that show screens. Changes nothing. Use this before set_site_passcode, remove_site_passcode or sign_out_site_visitors, and when the person asks what their site's passcode is.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| site | string | yes | The site's name as it appears in its address, e.g. `birthday-rsvp`: lowercase letters, numbers and hyphens. |

## set_site_passcode

<div data-tool="set_site_passcode"></div>

Put one passcode on a whole site, or change the one it has: every address of it shows an "Enter the passcode" page until a visitor enters the passcode. Changing it replaces the earlier passcode, which is not kept, and every visitor who unlocked the site with the old one must enter the new one. It is a shared passcode, not a login: anyone given it can open the site and pass it on, and it does not make saved data private per person. Confirm with the person before calling this. Do not ask them to say or type a passcode in chat, and do not repeat one back: leave passcode out to open the passcode screen (apps that show screens), or use generate: true and the reply's link. Send passcode only when you already hold one for the person's own use. The owner's key and this connector keep working on a protected site. To take the passcode off use remove_site_passcode; to make visitors enter it again without changing it use sign_out_site_visitors.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| generate | boolean |  | true: Simple Host sets a new numeric passcode. The person sees it on the passcode screen or at the reply's dashboard link, never in chat. |
| passcode | string |  | Send only a passcode you already hold for the person's own use, at least this server's minimum length. Never ask for it in chat. Leave out to open the screen or use generate. |
| site | string | yes | The site's name as it appears in its address, e.g. `birthday-rsvp`: lowercase letters, numbers and hyphens. |

## remove_site_passcode

<div data-tool="remove_site_passcode"></div>

ASK THE PERSON FIRST, before calling this: it takes the passcode off a site, so anyone with its address can open it again. Nothing on the site is deleted, and a new passcode can be set later with set_site_passcode. The person's own request to remove or change it is not consent on its own: tell them anyone with the link will be able to open the site once it is gone, then call again with confirm: true only after they agree in this conversation.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| confirm | boolean |  | The person agreed, in this conversation, to remove the passcode after being told anyone with the link will then be able to open the site. Omit or send false to ask first; nothing is removed until this is true. |
| site | string | yes | The site's name as it appears in its address, e.g. `birthday-rsvp`: lowercase letters, numbers and hyphens. |

## sign_out_site_visitors

<div data-tool="sign_out_site_visitors"></div>

Sign every visitor out of a passcode-protected site: each one sees "Enter the passcode" again and must enter the same passcode to open it. The passcode does not change and nothing is deleted. Use this when the person thinks the passcode reached someone it should not have and wants a fresh start; to stop the old passcode working, change it with set_site_passcode instead. Ask the person first. It fails on a site without a passcode.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| site | string | yes | The site's name as it appears in its address, e.g. `birthday-rsvp`: lowercase letters, numbers and hyphens. |

## set_site_access

<div data-tool="set_site_access"></div>

Set who can open a whole site. access `specific`: only the owner and the named viewers (grant_site_viewer adds them, and also turns this on); everyone else sees a sign-in page ("This site is private"), and a signed-in person who is not named sees "You do not have access". access `anyone`: open to anyone with the address again (the named viewers are kept for next time). Use this when the person wants only certain people to open a site ("only family", "only mom and dad", "just my team"): each named person signs in on the site with their email (an emailed code or Google) and nobody else gets in. It is not set_site_passcode (one shared code anyone can pass on), not set_visibility (List on my profile; every open site can be found by search engines) and not set_site_offline (closed to everyone). A site has named viewers or a passcode, never both. list_site_viewers shows the full list. ASK THE PERSON FIRST, naming the site and what changes. The owner's key and this connector keep working either way.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| access | string, one of `anyone`, `specific` | yes | `specific`: only the owner and the named viewers. `anyone`: everyone with the address. |
| site | string | yes | The site's name as it appears in its address, e.g. `birthday-rsvp`: lowercase letters, numbers and hyphens. |

## list_site_viewers

<div data-tool="list_site_viewers"></div>

Show who can open a site: its access (anyone or specific) and its named viewers. Use this when the person wants only certain people to open a site ("only family", "only mom and dad", "just my team"): each named person signs in on the site with their email (an emailed code or Google) and nobody else gets in. It is not set_site_passcode (one shared code anyone can pass on), not set_visibility (List on my profile; every open site can be found by search engines) and not set_site_offline (closed to everyone). A site has named viewers or a passcode, never both.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| site | string | yes | The site's name as it appears in its address, e.g. `birthday-rsvp`: lowercase letters, numbers and hyphens. |

## grant_site_viewer

<div data-tool="grant_site_viewer"></div>

Add people, by the email they will sign in with, to a site's named viewers, and set its access to `specific`: from then on only they and the owner can open the site, after signing in on it with that email (an emailed code, or Google for a Google address). One call does it: "make my family page only visible to mom@example.com and dad@example.com" is grant_site_viewer with both emails. Use this when the person wants only certain people to open a site ("only family", "only mom and dad", "just my team"): each named person signs in on the site with their email (an emailed code or Google) and nobody else gets in. It is not set_site_passcode (one shared code anyone can pass on), not set_visibility (List on my profile; every open site can be found by search engines) and not set_site_offline (closed to everyone). A site has named viewers or a passcode, never both. list_site_viewers shows the full list. ASK THE PERSON FIRST and use exactly the emails they gave; never guess an address. Tell them to send the people the site's address and to sign in with the email named here.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| emails | array of string | yes | The emails to add, exactly as the person gave them (e.g. ["mom@example.com", "dad@example.com"]). |
| site | string | yes | The site's name as it appears in its address, e.g. `birthday-rsvp`: lowercase letters, numbers and hyphens. |

## revoke_site_viewer

<div data-tool="revoke_site_viewer"></div>

Remove one person, by email, from a site's named viewers: that person can no longer open the site, and their next page view shows "You do not have access" instead of the site. Access does not change: removing the last viewer leaves the site open only to its owner until set_site_access sets it to anyone. Ask the person first.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| email | string | yes | The email to remove, as list_site_viewers shows it. |
| site | string | yes | The site's name as it appears in its address, e.g. `birthday-rsvp`: lowercase letters, numbers and hyphens. |
