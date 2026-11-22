// Package manage is saving, replacing, listing and deleting a site's secrets
// (routes.go), for the site's owner: the account, a member of its team
// (owner=<team>) or an editor of the site, as auth.EditorAllows says, with
// the product's write check on every change (auth.ScopeGate). Its rules
// (manage.go): the name, value, destination, who and attach checks, and the
// per-site limit (secrets.MaxPerSite). A value is never answered back.
package manage
