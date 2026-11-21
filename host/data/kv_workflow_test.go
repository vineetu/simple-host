package data

import (
	"context"
	"testing"

	"github.com/vsriram/simple-host/host/data/rules"
)

func TestPutKVGateBeforeReadingBody(t *testing.T) {
	read := false
	s := New(nil, Deps{})
	_, auth, err := s.PutKV(context.Background(), KVPutInput{Call: Call{Resource: rules.Resource{SitePasscode: "inherit"}}, Body: readProbe{&read}})
	if err != nil || auth == nil || auth.Code != "site_locked" || read {
		t.Fatalf("gate: auth=%+v err=%v read=%v", auth, err, read)
	}
}

type readProbe struct{ read *bool }

func (r readProbe) Read(p []byte) (int, error) { *r.read = true; return 0, nil }
