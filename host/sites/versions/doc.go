// Package versions is the Versions feature of Sites: a site's version list,
// making a stored version live again (Rollback), discarding a never-published
// version, the per-site keep-versions setting, and reading one version's files
// as text. It runs on the core's sites.Service (embedded); pruning by retention
// stays in the core because deploys and host/ops prune too.
package versions

import "github.com/vsriram/simple-host/host/sites"

// Service is the core site service with the versions workflows on it.
type Service struct{ *sites.Service }
