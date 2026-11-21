// Package hackhttp holds Simple Hack's HTTP routes, all mounted in routes.go.
// A handler reads the request, calls products/hack/app and writes the answer.
// Rules are in products/hack/rules (pure), SQL in products/hack/repository,
// workflows in products/hack/app; this package holds none of them.
package hackhttp
