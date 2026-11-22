// Package secrets is the Secrets core: a site's write-only keys (an API key
// for an outside service), sealed with AES-256-GCM under a master key file
// kept outside the database (SECRETS_KEY_FILE), the site id and the name
// bound as additional data. It owns the site_secrets table (secrets.go) and
// the sealing (seal.go). It sits above the modules: its features use the
// cores of Access, Sites and Data, and nothing below imports it. Each feature
// is a package beside it: manage (save, replace, list and delete, for the
// site's owner) and call (a site's page asks for a request to the key's one
// destination; the key is added on the way out).
//
// Who sees: nobody ever sees a value again, nor its ciphertext, length or any
// part of it. The site's owner (and its editors, as auth.EditorAllows says)
// sees each secret's name, destination, who may use it, how it is attached,
// its dates and its use count. Nothing of a secret goes into a site export,
// the account-data export, logs, analytics or an error message. Open is the
// one function that reads a value back, and only call uses it.
package secrets
