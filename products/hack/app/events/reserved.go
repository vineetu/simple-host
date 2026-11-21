package events

import "github.com/vsriram/simple-host/host/rules/sites"

// extraEventReserved is the extra names refused for an event slug, on top of labelReservedForNew / judgeHandle.
var extraEventReserved = map[string]bool{
	"www": true, "api": true, "admin": true, "app": true,
	"judge": true, "judges": true, "vote": true, "results": true,
	"e": true, "events": true, "event": true, "join": true,
	"signin": true, "sign-in": true, "sites": true, "site": true,
	"team": true, "teams": true, "manage": true, "new": true,
	"help": true, "docs": true, "status": true, "mail": true,
	"blog": true, "support": true, "report": true, "static": true,
	"cdn": true, "mcp": true, "auth": true,
	"mta-sts": true, "autoconfig": true, "autodiscover": true, "webmail": true,
	// Names the certificate issuer never issues for (deploy/site-certs/
	// issue.sh RESERVED): an event under one could never have team sites.
	"lab": true, "cname": true, "test": true, "dev": true, "localhost": true,
}

// NameReserved is whether name may not be an event slug or a team slug: the
// platform's reserved labels plus extraEventReserved.
func NameReserved(name string) bool {
	return extraEventReserved[name] || sites.LabelReservedForNew(name)
}
