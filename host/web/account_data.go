package web

import (
	"strings"

	"github.com/vsriram/simple-host/host/access/auth"
)

// SupportText puts this install's contact (auth.SupportContact) where s
// names the hosted service's support address.
func SupportText(s string) string {
	if auth.SupportContact == "support@simple-host.app" {
		return s
	}
	return strings.ReplaceAll(s, "support@simple-host.app", auth.SupportContact)
}
