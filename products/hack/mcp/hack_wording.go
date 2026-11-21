package hackmcp

import "strings"

const hackPhotoTip = "Compress photos before deploying: about 1600 px wide, WebP or JPEG at about 80% quality; phone photos are often 4–12 MB."

// Host refusal hints are written for the owner's connector: keep the REST
// refusal and code; Hack callers reconnect in the app.
func hackStorageErrorMessage(message string) string {
	if strings.Contains(message, "[key_expired]") || strings.Contains(message, "[key_expired_idle]") {
		first, _, _ := strings.Cut(message, "\n")
		return first + "\nReconnect the Simple Hack connector in the app; do not share a key in chat."
	}
	return message
}

// hackInstanceReplace is the one-line replace shared by the instance
// instructions and tools. On simple-hack.app itself APIHost is the same, so it
// is an identity replace; a self-hosted APIHost makes it a real substitution.
func hackInstanceReplace(text, apiHost string) string {
	if apiHost != "" {
		text = strings.ReplaceAll(text, "simple-hack.app", apiHost)
	}
	return text
}

const hackStorageUsageDescription = "Show how much of a site's saved-data allowance is used: on Simple Host, KV and SQLite together (10,000,000 bytes) and files separately (10 MB); on Simple Hack, one 1,000,000-byte pool for all three. The site's own pages and images are counted elsewhere (list_sites)."
