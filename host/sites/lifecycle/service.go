package lifecycle

import "github.com/vsriram/simple-host/host/sites"

// Service runs the lifecycle workflows on the instance's site service.
type Service struct{ *sites.Service }
