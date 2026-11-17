package handler

import (
	"context"
	"encoding/json"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ---- One name list with the peer instance ---------------------------------
//
// simple-host.app's /v1/events hands out <name>.simple-hack.app records for
// self-hosted events; simple-hack.app hands out <event>.simple-hack.app for
// hosted ones. Each asks the other (EVENT_NAME_PEER, loopback) before taking a
// name: GET /internal/names/taken?zone=<zone>&name=<name> → {"taken": bool}.

// RegisterNamePeer adds GET /internal/names/taken, answered by taken for names
// under any of zones (other zones: not taken). nginx never forwards
// /internal/; the handler also refuses anything but a direct loopback caller.
func RegisterNamePeer(mux *http.ServeMux, zones []string, taken func(ctx context.Context, zone, name string) (bool, error)) {
	ours := map[string]bool{}
	for _, z := range zones {
		if z = strings.ToLower(strings.TrimSpace(z)); z != "" {
			ours[z] = true
		}
	}
	mux.HandleFunc("GET /internal/names/taken", func(w http.ResponseWriter, r *http.Request) {
		if !directLoopback(r) {
			http.NotFound(w, r)
			return
		}
		name := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("name")))
		zone := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("zone")))
		if !ours[zone] || name == "" {
			writeJSON(w, http.StatusOK, map[string]bool{"taken": false})
			return
		}
		t, err := taken(r.Context(), zone, name)
		if err != nil {
			log.Printf("name peer: %s: %v", name, err)
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"taken": t})
	})
}

// directLoopback: the request came straight from this box, not through nginx
// (which always adds X-Forwarded-For / X-Real-IP).
func directLoopback(r *http.Request) bool {
	if r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("X-Real-IP") != "" {
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// NamePeerClient asks the peer at base (http://127.0.0.1:<port>) whether it
// holds name under zone. Any failure is an error: callers refuse the name.
func NamePeerClient(base string) func(ctx context.Context, zone, name string) (bool, error) {
	client := &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return func(ctx context.Context, zone, name string) (bool, error) {
		u := base + "/internal/names/taken?zone=" + url.QueryEscape(zone) + "&name=" + url.QueryEscape(name)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return false, err
		}
		res, err := client.Do(req)
		if err != nil {
			return false, err
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			return false, &peerStatusError{res.StatusCode}
		}
		var out struct {
			Taken *bool `json:"taken"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(nil, res.Body, 4096)).Decode(&out); err != nil || out.Taken == nil {
			return false, &peerStatusError{0}
		}
		return *out.Taken, nil
	}
}

type peerStatusError struct{ status int }

func (e *peerStatusError) Error() string {
	if e.status == 0 {
		return "name peer: unreadable answer"
	}
	return "name peer: status " + http.StatusText(e.status)
}

// DirUsage returns the bytes of regular files under dir, measured at most
// every ten minutes (a walk of the hackathon platform's team sites, for
// HACK_INSTANCE_BUDGET_GB).
func DirUsage(dir string) func(ctx context.Context) (int64, error) {
	var (
		mu     sync.Mutex
		last   time.Time
		cached int64
	)
	return func(ctx context.Context) (int64, error) {
		mu.Lock()
		defer mu.Unlock()
		if !last.IsZero() && time.Since(last) < 10*time.Minute {
			return cached, nil
		}
		var total int64
		err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if d.Type().IsRegular() {
				if info, ierr := d.Info(); ierr == nil {
					total += info.Size()
				}
			}
			return nil
		})
		if err != nil {
			return 0, err
		}
		cached, last = total, time.Now()
		return total, nil
	}
}
