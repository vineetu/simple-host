// Package gate is what stands between a person and a site's pages and saved
// data, and every way through it: the site gate (SiteGate) and the data-route
// gates (StorageCaller, PasscodeGate); site passcodes and their unlock
// (passcode*.go); named viewers and their sign-in form (viewers.go); visitor
// sign-in by emailed code (visitoremail.go) and by Google or a company OIDC,
// with the SSO hop between the app's address and a site's (oauth.go, sso.go);
// visitor sessions (visitorsession.go); and the visitor tools the connector
// serves (visitor_tools.go). These call each other (the gate starts the SSO
// hop, the hop renders the gate's pages), so they are one unit. The verdicts
// are the core's: access.DecideVisit and Service.VisitorSession.
package gate
