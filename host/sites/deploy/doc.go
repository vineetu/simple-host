// Package deploy is Sites' publishing workflow: the first deploy (Create),
// a later one (Update, keep=1 patches and photo merges), the archive and
// JSON entry point (Deploy), upload preflight and file preparation, the
// published _redirects check and preview-site expiry. It runs on the core
// service (Service embeds *sites.Service) and never imports another
// feature of host/sites.
package deploy

import "github.com/vsriram/simple-host/host/sites"

// Service is the core site service with the deploy workflow on it.
type Service struct{ *sites.Service }
