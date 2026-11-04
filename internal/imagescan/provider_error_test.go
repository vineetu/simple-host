package imagescan

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestSafeProviderErrors(t *testing.T) {
	for _, op := range []string{"match", "report"} {
		srv := server(t, 503, `private-response-uploader@example.com`, nil)
		p := &PhotoDNA{MatchURL: srv.URL, ReportURL: srv.URL}
		var err error
		if op == "match" {
			_, err = p.Match(context.Background(), &Image{Data: []byte("image")})
		} else {
			_, err = p.Report(context.Background(), Report{OrgName: "org", ReporterName: "operator", ReporterEmail: "operator@example.com", Files: []ReportFile{{Data: []byte("image")}}})
		}
		safe := SafeError(op, err)
		if safe == nil || !strings.Contains(safe.Error(), op+": HTTP 503: temporary provider failure") || strings.Contains(safe.Error(), "private-response") {
			t.Fatalf("unsafe or incomplete error: %v", safe)
		}
		if !IsRetryable(safe) {
			t.Fatalf("lost retry classification: %v", safe)
		}
	}
	safe := SafeError("match", errors.New("secret body and uploader@example.com"))
	if strings.Contains(safe.Error(), "@") || strings.Contains(safe.Error(), "secret") {
		t.Fatalf("unsafe custom provider error: %v", safe)
	}
}
