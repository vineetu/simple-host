package namepeer

import (
	"context"
	"log"
	"net"
	"net/http"
	"strings"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/sites/eventdns"
)

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
			httpx.WriteJSON(w, http.StatusOK, map[string]bool{"taken": false})
			return
		}
		t, err := taken(r.Context(), zone, name)
		if err != nil {
			log.Printf("name peer: %s: %v", name, err)
			httpx.WriteInternal(w)
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]bool{"taken": t})
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
// holds name under zone (eventdns.NamePeer). Any failure is an error: callers
// refuse the name.
func NamePeerClient(base string) func(ctx context.Context, zone, name string) (bool, error) {
	return eventdns.NewNamePeer(base).Taken
}
