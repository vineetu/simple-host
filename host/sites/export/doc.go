// Package export resolves which site an export archive, a signed export
// link or the operator's whole-instance export reads, and the kept version a
// team preview shows. The archives themselves are written by host/ops
// (SiteArchive); Register (routes.go) mounts the export routes.
package export

import "github.com/vsriram/simple-host/host/sites"

// Service runs the look-ups on the instance's site service.
type Service struct{ *sites.Service }
