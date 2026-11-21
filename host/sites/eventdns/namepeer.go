package eventdns

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"time"
)

// NamePeer asks the peer instance at base (http://127.0.0.1:<port>,
// EVENT_NAME_PEER) whether it holds a name under a zone: GET
// /internal/names/taken?zone=<zone>&name=<name> → {"taken": bool}.
type NamePeer struct {
	base   string
	client *http.Client
}

// NewNamePeer is the peer at base.
func NewNamePeer(base string) *NamePeer {
	return &NamePeer{base: base, client: &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

// Taken reports whether the peer holds name under zone. Any failure is an
// error: callers refuse the name.
func (p *NamePeer) Taken(ctx context.Context, zone, name string) (bool, error) {
	u := p.base + "/internal/names/taken?zone=" + url.QueryEscape(zone) + "&name=" + url.QueryEscape(name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return false, err
	}
	res, err := p.client.Do(req)
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

type peerStatusError struct{ status int }

func (e *peerStatusError) Error() string {
	if e.status == 0 {
		return "name peer: unreadable answer"
	}
	return "name peer: status " + http.StatusText(e.status)
}
