package profile

import (
	"strconv"
	"strings"

	. "github.com/vsriram/simple-host/host/access/featuretest"
)

// hostedDomainLeak reports every captured string (sign-in codes, the
// sign-in link, and every notice's to|subject|text) that names either
// hosted domain or the hosted support address, case-insensitively.
func hostedDomainLeak(mb *Mailbox, links []string) []string {
	var hits []string
	check := func(label, s string) {
		low := strings.ToLower(s)
		if strings.Contains(low, "simple-hack.app") || strings.Contains(low, "simple-host.app") {
			hits = append(hits, label+": "+s)
		}
	}
	mb.Mu.Lock()
	for addr, code := range mb.Codes {
		check("sign-in code to "+addr, code)
	}
	for _, n := range mb.Notices {
		check("notice", n)
	}
	mb.Mu.Unlock()
	for i, l := range links {
		check("link "+strconv.Itoa(i), l)
	}
	return hits
}
