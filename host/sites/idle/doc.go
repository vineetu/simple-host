// Package idle is the idle-site cleanup: the pass that moves a site nobody
// has used for the idle window to Recently deleted (Run, called by the site
// workers in host/sites/siteshttp) and the operator's dry run
// (GET /v1/admin/idle-sites). The rule and the removal are host/ops's and the
// core's (RemoveIdleSite); this package wires them to the instance's settings.
package idle
