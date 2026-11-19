# Photo and archive uploads

## create_upload_link

<div data-tool="create_upload_link"></div>

Opens a photo picker in the chat (apps that show MCP Apps); elsewhere returns photos_url to give the person. Call it as soon as a page needs the person's own photos (menu dishes, a listing, portfolio, team) and the site exists (publish the page first), even before they mention photos, instead of building an upload page or asking them to attach, convert, resize, or host photos. They pick any format on phone or computer; each is shrunk into images/, keeping other files. When they say done, get_site and place them. Photos visitors share are the gallery recipe instead. archive: true gives shell agents a whole-site replacement upload. Each call makes a new link.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| archive | boolean |  | Shell archive upload that replaces the whole site. |
| owner | string |  | Act on the sites of this team (its handle from list_teams) instead of your own. Leave it out for your own sites. You act with your own sign-in; the team's other members see what you change. |
| site | string | yes | The site's name as it appears in its address, e.g. `birthday-rsvp`: lowercase letters, numbers and hyphens. |
