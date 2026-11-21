package accesstest

import (
	"testing"

	"github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/accesshttp"
)

const (
	opaqueOwner = "1b4e28ba-2fa1-11d2-883f-0016d3cca427"
	opaqueSite  = "2b4e28ba-2fa1-11d2-883f-0016d3cca427"
)

// The connector hands tools the same opaque visitor reference that
// notifications carry, so one visitor has one reference everywhere.
func TestConnectorUsesSiteVisitorKey(t *testing.T) {
	h := &accesshttp.AccessHandler{ExportKey: []byte("accesstest-export-key-0123456789")}
	connector := &accesshttp.ConnectorHandler{}
	connector.SetVisitorReferences(h)
	ref, err := connector.MCPConfig.VisitorRefs.Encode(opaqueSite, opaqueOwner)
	expected, _ := access.NewVisitorReferences(h.ExportKey).Encode(opaqueSite, opaqueOwner)
	if err != nil || ref != expected {
		t.Fatal("connector and notification references differ")
	}
}
