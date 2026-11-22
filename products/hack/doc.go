// Package hack is Simple Hack's composition root: Extension plugs the product
// into the platform, and HackHandler builds the core (products/hack/core) and
// registers each feature package's routes in Register (compose_routes.go). The
// workers that span features (mail delivery, limiter sweeps) live here too.
package hack
