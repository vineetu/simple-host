package model

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
