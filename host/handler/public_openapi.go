package handler

import "github.com/vsriram/simple-host/host/webassets"

// publicOpenAPISpec selects operations from the single contract by audience
// (webassets.PublicOpenAPISpec, shared with the docs reference generator).
func publicOpenAPISpec(source []byte, audience string) ([]byte, error) {
	return webassets.PublicOpenAPISpec(source, audience)
}

func openAPIMethod(method string) bool { return webassets.OpenAPIMethod(method) }
