package storage

import "strings"

// BaseEmail is the address one person is known by for block lists and one
// entry per person: lower case, with a "+tag" dropped from the part before
// the @ (ann+2@example.com is ann@example.com). "" stays "".
func BaseEmail(email string) string {
	email = strings.ToLower(strings.TrimSpace(email))
	at := strings.LastIndex(email, "@")
	if at < 0 {
		return email
	}
	local := email[:at]
	if plus := strings.Index(local, "+"); plus > 0 {
		local = local[:plus]
	}
	return local + email[at:]
}

// LegacySaverKeys normalizes an address and builds allow and block keys.
func LegacySaverKeys(email string) (string, []string, []string) {
	email = strings.ToLower(strings.TrimSpace(email))
	keys := []string{email}
	if at := strings.LastIndex(email, "@"); at >= 0 {
		keys = append(keys, email[at:])
	}
	blockKeys := keys
	if base := BaseEmail(email); base != email {
		blockKeys = append([]string{base}, keys...)
	}
	return email, keys, blockKeys
}
