// Package call is a site's page using a saved key: the page asks Simple Host
// for a request to the key's one destination host, and Simple Host adds the
// key on the way out and streams the answer back (routes.go). Nothing is
// specific to a provider: method, path, query and body are the page's.
//
// Who may call is the key's who, decided by Data's site gate (passcode,
// named viewers, the X-SH-CSRF header) and identity (call.go: admit). The
// request goes out only over HTTPS to port 443 of the stored destination,
// through the one outbound guard (host/outbound), which refuses every
// non-public address and this server's own at connect time; a redirect is
// refused, never handed to the browser. Every method spends the key, so
// every call is a change to every gate (accounts.ReadRequest). The key, the
// request's headers and its URL are never logged or answered.
package call
