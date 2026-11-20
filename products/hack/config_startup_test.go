package hack

import (
	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/extension"
	"github.com/vsriram/simple-host/host/server"
	"strings"
	"testing"
)

func TestBadProductSettingStopsStartup(t *testing.T) {
	t.Setenv("HACK_EVENT_ICON_MAX_BYTES", "bad")
	app, err := server.Build(config.Config{DataDir: t.TempDir()}, []*extension.Extension{Extension()}, nil)
	if app != nil {
		app.Close()
		t.Fatal("startup accepted bad product setting")
	}
	if err == nil || !strings.Contains(err.Error(), `HACK_EVENT_ICON_MAX_BYTES="bad": want a whole number from 1 to 4194304`) {
		t.Fatalf("startup error = %v", err)
	}
}
