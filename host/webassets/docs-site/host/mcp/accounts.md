# Account

## who_am_i

<div data-tool="who_am_i"></div>

Return the Simple Host account this connection acts as: its handle (the &lt;handle&gt; in its page https://&lt;handle&gt;.simple-host.app/ and its site addresses https://&lt;site&gt;.&lt;handle&gt;.simple-host.app/) and its public page listing its sites. Email is returned only when include_email is true.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| include_email | boolean |  | Set true only when the person asks which email they are signed in with. |

## set_bio

<div data-tool="set_bio"></div>

Set the short plain-text bio shown on the person's public page (https://&lt;handle&gt;.simple-host.app/, the showcase of their sites). An empty string clears it; the limit is about 280 characters. It replaces the current bio, which is not kept anywhere, so ask the person before replacing or clearing a bio they wrote (their public page shows the current one).

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| bio | string | yes | Plain text, or empty to clear the bio. |

## set_showcase_site

<div data-tool="set_showcase_site"></div>

Pin a site to the top of the person's public page (the showcase of their sites), or set its position. Pinned sites come first, then smaller order numbers. Sites with List on my profile off, offline sites, and passcode-protected sites never appear there, pinned or not. Supply pinned, order, or both.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| order | integer |  | Manual position among the pinned or unpinned sites: smaller numbers come first. |
| owner | string |  | Act on the sites of this team (its handle from list_teams) instead of your own. Leave it out for your own sites. You act with your own sign-in; the team's other members see what you change. |
| pinned | boolean |  | true pins the site to the top of the showcase; false unpins it. |
| site | string | yes | The site's name as it appears in its address, e.g. `birthday-rsvp`: lowercase letters, numbers and hyphens. |

## create_team

<div data-tool="create_team"></div>

Create a team: an account that owns sites, which people join. You become its first member. Its name becomes its address, https://&lt;site&gt;.&lt;name&gt;.simple-host.app/, so it follows the address rules: lowercase letters, numbers and hyphens, no dots. A team cannot be renamed later. Ask the person for the name first.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| name | string | yes | The team's name and address: 1 to 39 lowercase letters, numbers or hyphens. |

## list_teams

<div data-tool="list_teams"></div>

List the teams you belong to, with each team's id and handle, and the invitations to join a team that wait for your answer. Pass a team's handle as owner on site and storage tools to work on its sites.

## list_team_members

<div data-tool="list_team_members"></div>

List the members of a team you belong to (each one's handle, name, who added them and when) and its invitations still waiting for an answer.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| team | string | yes | The team's handle, as list_teams shows it (the &lt;team&gt; in https://&lt;site&gt;.&lt;team&gt;.simple-host.app/). |

## add_team_member

<div data-tool="add_team_member"></div>

Invite a person to a team you belong to, by their email. Nobody becomes a member until they accept: they see the invitation when signed in with that email (who_am_i or list_teams) and accept or decline it. Nothing is sent to them, so tell them yourself. The answer is the same whether or not anyone has that email. Every member can publish, change and delete all the team's sites and invite or remove members, so confirm the person and the team with the person you are helping first. Invitations lapse after 14 days; a team may have 20 waiting.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| email | string | yes | The person's sign-in email. |
| team | string | yes | The team's handle, as list_teams shows it (the &lt;team&gt; in https://&lt;site&gt;.&lt;team&gt;.simple-host.app/). |

## cancel_team_invitation

<div data-tool="cancel_team_invitation"></div>

Cancel an invitation of a team you belong to that is still waiting for an answer (its id from list_team_members).

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| invitation | string | yes | The invitation's id, from list_team_members. |
| team | string | yes | The team's handle, as list_teams shows it (the &lt;team&gt; in https://&lt;site&gt;.&lt;team&gt;.simple-host.app/). |

## answer_team_invitation

<div data-tool="answer_team_invitation"></div>

Accept or decline an invitation to join a team that waits for you (who_am_i or list_teams lists them). Accepting makes you a member: you can then publish, change and delete all the team's sites with owner=&lt;team&gt;, as every member can. Ask the person which answer they want first.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| answer | string, one of `accept`, `decline` | yes | accept or decline. |
| invitation | string | yes | The invitation's id. |

## remove_team_member

<div data-tool="remove_team_member"></div>

Remove a member from a team you belong to (yourself included), by handle or account id. Their access to the team's sites stops on their next request, and invitations they sent are withdrawn. The last member cannot leave: to end the team, delete its sites, then the team. Ask before removing anyone.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| person | string | yes | The member's handle or account id, from list_team_members. |
| team | string | yes | The team's handle, as list_teams shows it (the &lt;team&gt; in https://&lt;site&gt;.&lt;team&gt;.simple-host.app/). |

## delete_team

<div data-tool="delete_team"></div>

Delete a team you belong to. Only a team that owns no sites can be deleted (sites in Recently deleted count): delete its sites first. Its name is retired and never given out again. Ask the person before deleting.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| team | string | yes | The team's handle, as list_teams shows it (the &lt;team&gt; in https://&lt;site&gt;.&lt;team&gt;.simple-host.app/). |
