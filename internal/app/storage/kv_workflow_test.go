package storage

import (
	"context"
	"encoding/json"
	"testing"

	rules "github.com/vsriram/simple-host/internal/rules/storage"
)

func TestPutKVGateBeforeReadingBody(t *testing.T) {
	read := false
	s := New(nil, Deps{
		PasscodeAllows: func() bool { return false },
		ReadKVValue:    func() (json.RawMessage, error) { read = true; return nil, nil },
	})
	_, auth, err := s.PutKV(context.Background(), KVPutInput{Call: Call{Resource: rules.Resource{SitePasscode: "inherit"}}})
	if err != nil || auth == nil || auth.Code != "site_locked" || read {
		t.Fatalf("gate: auth=%+v err=%v read=%v", auth, err, read)
	}
}
