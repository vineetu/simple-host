package web

import "github.com/vsriram/simple-host/host/webassets"

// publicOpenAPISpec selects operations from the single contract by audience
// (webassets.PublicOpenAPISpec).
func publicOpenAPISpec(source []byte, audience string) ([]byte, error) {
	return webassets.PublicOpenAPISpec(source, audience)
}

func openAPIMethod(method string) bool { return webassets.OpenAPIMethod(method) }
