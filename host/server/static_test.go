package server

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/static"
	"github.com/vsriram/simple-host/host/storage"
)

func TestStaticProcessWithoutDatabase(t *testing.T) {
	if os.Getenv("D4_STATIC_CHILD") == "1" {
		os.Exit(runStaticCommand(nil, nil))
	}
	for _, saved := range []bool{true, false} {
		t.Run(map[bool]string{true: "saved", false: "no-copy"}[saved], func(t *testing.T) {
			dir, state := t.TempDir(), t.TempDir()
			current := filepath.Join(dir, "by-id", "owner", "page", "current")
			if err := os.MkdirAll(current, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(current, "index.html"), []byte("public page"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := storage.WriteInstallID(dir, "static-test-install"); err != nil {
				t.Fatal(err)
			}
			lock, err := storage.LockMaintenance(dir, false)
			if err != nil {
				t.Fatal(err)
			}
			lock.Close()
			legacy := filepath.Join(filepath.Dir(current), "unlisted")
			if err := os.WriteFile(legacy, []byte("preserve"), 0600); err != nil {
				t.Fatal(err)
			}
			if saved {
				copy := static.New(static.Options{DataDir: dir, StateDir: state, Product: "simple-host", Load: func(context.Context) (model.SiteTable, time.Time, error) {
					return model.SiteTable{Owners: []model.RouteOwner{{ID: "owner", Handle: "alice"}}, Sites: []model.RouteSite{{ID: "page", UserID: "owner", Name: "page", ActiveVersion: 1}}}, time.Now(), nil
				}})
				if err := copy.Refresh(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			port := strings.Split(listener.Addr().String(), ":")[1]
			listener.Close()
			cmd := exec.Command(os.Args[0], "-test.run=^TestStaticProcessWithoutDatabase$")
			cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "D4_STATIC_CHILD=1", "DB_DSN=postgres://static:static@127.0.0.1:0/unreachable?sslmode=disable&connect_timeout=1", "DATA_DIR=" + dir, "STATIC_STATE_DIR=" + state, "STATIC_BIND=127.0.0.1", "STATIC_PORT=" + port, "STATIC_API_URL=http://127.0.0.1:0", "SITE_DOMAIN=example.test", "PERSON_HOSTS=serve", "SITE_HOSTS=serve"}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			t.Cleanup(func() {
				if cmd.ProcessState == nil {
					_ = cmd.Process.Kill()
					<-done
				}
			})
			client := &http.Client{Timeout: time.Second}
			deadline := time.Now().Add(10 * time.Second)
			var response *http.Response
			for time.Now().Before(deadline) {
				req, _ := http.NewRequest("GET", "http://127.0.0.1:"+port+"/", nil)
				req.Host = "page.alice.example.test"
				response, err = client.Do(req)
				if err == nil {
					break
				}
				select {
				case err := <-done:
					t.Fatalf("Static exited before serving: %v", err)
				default:
				}
				time.Sleep(10 * time.Millisecond)
			}
			if err != nil {
				t.Fatalf("Static did not start: %v", err)
			}
			body, _ := io.ReadAll(response.Body)
			response.Body.Close()
			if saved {
				if response.StatusCode != 200 || string(body) != "public page" {
					t.Fatalf("saved page: %d %q", response.StatusCode, body)
				}
			} else if response.StatusCode != 503 || response.Header.Get("Retry-After") == "" {
				t.Fatalf("no-copy response: %d", response.StatusCode)
			}
			if content, err := os.ReadFile(legacy); err != nil || string(content) != "preserve" {
				t.Fatal("Static modified site files")
			}
			if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("SIGTERM exit: %v", err)
				}
			case <-time.After(12 * time.Second):
				t.Fatal("Static did not drain and exit")
			}
		})
	}
}
