package sites

import (
	"errors"
	"net"
	"regexp"
	"strings"
)

var domainLabel = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// DomainShape: a lowercase custom domain is ASCII DNS labels (1 to 63 of
// [a-z0-9-], no hyphen at either end), at least two of them, at most 253
// characters, and not an IP address.
func DomainShape(s string) error {
	if len(s) > 253 || !strings.Contains(s, ".") || net.ParseIP(s) != nil {
		return errors.New("domain must be a name with a '.', at most 253 characters, not an IP address")
	}
	for _, label := range strings.Split(s, ".") {
		if !domainLabel.MatchString(label) {
			return errors.New("domain labels must be 1 to 63 of a-z, 0-9 and '-', not starting or ending with '-'")
		}
	}
	return nil
}
