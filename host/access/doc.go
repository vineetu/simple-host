// Package access owns who may do what: accounts, keys, sign-in, the
// connector, site passcodes, named viewers, site editors and teams. It
// imports no other module: sites and data import it and call it directly.
//
// Its door: repo/people.go (what other modules read and change
// of an account, the users table's only reader and writer), product.go (what
// a product asks of accounts) and the Service methods.
//
// Its rules, one function each:
//   - the visitor gate, for pages, saved-data routes, sign-in routes and
//     visitor tools: DecideVisit, PasscodeAdmits and ViewersAdmit
//     (visit_gate.go); the edges measure the request and render the verdict;
//   - who acts as a site's owner: ActsAsOwner and Service.ResolveOwner (teams.go);
//   - an account is suspended: Service.UserSuspended (and model.User.Suspended
//     as loaded by repo);
//   - which routes an editor or a deploy-only key may call: EditorAllows and
//     DeployKeyAllows (auth/scope.go), which the MCP tool list also asks;
//   - the named-viewer list and count: ViewerEmails, NewViewers,
//     NamedViewersRefusal and PasscodeRefusal (site_gates.go).
//
// Its HTTP routes are in accesshttp (routes.go).
package access
