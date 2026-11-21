package repo

// The SQL forms of the site-state predicates on model.Site
// (host/model/site.go), each defined once here. Queries name the sites table
// s and, where the owner's account matters, join it as o (ownerFrom), so the
// condition and the rows it guards stay in one statement.

// sqlTakenDown is model.Site.Suspended(): the site is down by the operator,
// by its own flag or through its owner's account.
const sqlTakenDown = `(s.suspended_at IS NOT NULL OR o.suspended_at IS NOT NULL)`

// sqlSiteLive is the site's own half of model.Site.Live(): not offline and
// not taken down by its own flag (a query without the owner join adds the
// account's half itself). a is the sites table's alias with its dot.
func sqlSiteLive(a string) string { return a + "offline_at IS NULL AND " + a + "suspended_at IS NULL" }

// sqlNotGated is !model.Site.Gated(): no passcode and open to anyone.
const sqlNotGated = `s.passcode_enc IS NULL AND s.access = 'anyone'`
