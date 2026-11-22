package kv

import (
	"context"
	"testing"

	"github.com/vsriram/simple-host/host/data"

	model "github.com/vsriram/simple-host/host/model"
)

func TestPutKVGateBeforeReadingBody(t *testing.T) {
	read := false
	s := data.New(nil, data.Deps{})
	_, auth, err := PutKV(s, context.Background(), KVPutInput{Call: data.Call{Resource: model.Resource{SitePasscode: "inherit"}}, Body: readProbe{&read}})
	if err != nil || auth == nil || auth.Code != "site_locked" || read {
		t.Fatalf("gate: auth=%+v err=%v read=%v", auth, err, read)
	}
}

type readProbe struct{ read *bool }

func (r readProbe) Read(p []byte) (int, error) { *r.read = true; return 0, nil }
