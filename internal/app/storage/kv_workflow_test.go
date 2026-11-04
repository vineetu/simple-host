package storage

import (
	"context"
	"testing"

	rules "github.com/vsriram/simple-host/internal/rules/storage"
)

func TestPutKVGateBeforeReadingBody(t *testing.T) {
	read := false
	s := New(nil, Deps{
		PasscodeAllows: func() bool { return false },
	})
	_, auth, err := s.PutKV(context.Background(), KVPutInput{Call: Call{Resource: rules.Resource{SitePasscode: "inherit"}}, Body: readProbe{&read}})
	if err != nil || auth == nil || auth.Code != "site_locked" || read {
		t.Fatalf("gate: auth=%+v err=%v read=%v", auth, err, read)
	}
}

type readProbe struct{ read *bool }

func (r readProbe) Read(p []byte) (int, error) { *r.read = true; return 0, nil }
