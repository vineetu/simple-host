package hackmcp

import (
	"strings"

	"github.com/vsriram/simple-host/host/mcp"
)

func hackPhotoWording(description string) string {
	description = strings.Replace(description, "Show how much of a site's saved-data allowance is used: KV and SQLite together (10,000,000 bytes) and files separately (10 MB). The site's own pages and images are counted elsewhere (list_sites).", hackStorageUsageDescription, 1)
	description = strings.Replace(description, mcp.StorageOwnerPhotos, ", a gallery you fill", 1)
	return strings.Replace(description, mcp.StorageGalleryLookalike, hackStorageGalleryLookalike, 1)
}

const hackTeamOwnerSentence = "\"own\" means the signed-in person who added the entry; \"owner\" means the team: these tools with the selected team, and a team member signed in on the team site's own address with the email they use on Simple Hack, where a page can act as an admin page (all orders, set a status). That stops at the team's submission deadline, with the team's other changes; after it a member there is an ordinary visitor. \"nobody\" keeps data to these tools only. "

const hackEventOwnerSentence = "\"own\" means the signed-in person who added the entry; \"owner\" means the event's organisers: these hack_event_storage_* tools, and an organiser signed in on the event website's own address with the email they use on Simple Hack, where a page can act as an admin page (all questions, mark one answered). That stops when the event ends. \"nobody\" keeps data to these tools only. "

const hackStorageGalleryLookalike = "a gallery you fill is public on files, one visitors add to is wall on files"
const hackPhotoTip = "Compress photos before deploying: about 1600 px wide, WebP or JPEG at about 80% quality; phone photos are often 4–12 MB."

// Shared Host refusal hints are written for the owner's own connector. Keep
// the actual REST refusal and code; Hack callers reconnect in the app.
func hackStorageErrorMessage(message string) string {
	if strings.Contains(message, "[key_expired]") || strings.Contains(message, "[key_expired_idle]") {
		first, _, _ := strings.Cut(message, "\n")
		return first + "\nReconnect the Simple Hack connector in the app; do not share a key in chat."
	}
	return message
}

// hackInstanceReplace is hackInstanceInstructions' one-line replace, shared
// with hackInstanceTools/hackInstanceToolMap below. On simple-hack.app
// itself cfg.APIHost is "simple-hack.app" too, so this is an identity
// replace there -- same as hackInstanceInstructions already relied on for
// the top-level instructions text; a self-hosted instance's own APIHost
// makes it a real substitution.
func hackInstanceReplace(text, apiHost string) string {
	if apiHost != "" {
		text = strings.ReplaceAll(text, "simple-hack.app", apiHost)
	}
	return text
}

const hackStorageUsageDescription = "Show how much of a site's saved-data allowance is used: on Simple Host, KV and SQLite together (10,000,000 bytes) and files separately (10 MB); on Simple Hack, one 1,000,000-byte pool for all three. The site's own pages and images are counted elsewhere (list_sites)."
