package eventdns

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"time"
)

// LiveProbe reports whether a hostname already serves something.
//
// Listing the zone is not enough: these domains carry a wildcard A record, so a
// live product can be served at <name>.<domain> with no record of its own, and
// claiming that name would override the wildcard and take it offline. So fetch
// the candidate and a name that certainly does not exist; if they answer
// differently, something is there.
type LiveProbe struct {
	HTTP *http.Client
}

func NewLiveProbe() *LiveProbe {
	return &LiveProbe{HTTP: &http.Client{
		Timeout: 8 * time.Second,
		// A redirect is itself evidence the name is in use, so stop and look at
		// it rather than following it somewhere unrelated.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// InUse compares https://<name>.<domain>/ with a control hostname on the same
// domain. What an unused name does varies by domain (a 404 behind a wildcard,
// a failed connection on a bare domain), so a failed request is a result, not
// an error: only a candidate that behaves differently from its control is in use.
func (p *LiveProbe) InUse(ctx context.Context, domain, name string) (bool, error) {
	control, err := randomLabel()
	if err != nil {
		return true, err
	}

	// Each candidate needs a control of the same shape: a wildcard certificate
	// covers one label, so <name>.<domain> can answer while
	// sites.<name>.<domain> cannot connect, whether or not anything is there.
	pairs := []struct{ candidate, control string }{
		{name + "." + domain, control + "." + domain},
		{"sites." + name + "." + domain, "sites." + control + "." + domain},
	}
	for _, pair := range pairs {
		controlStatus, controlErr := p.status(ctx, pair.control)
		status, err := p.status(ctx, pair.candidate)
		if (err == nil) != (controlErr == nil) {
			return true, nil
		}
		if err == nil && status != controlStatus {
			return true, nil
		}
	}
	return false, nil
}

func (p *LiveProbe) status(ctx context.Context, host string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+host+"/", nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "simple-host-event-name-check")
	res, err := p.HTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	return res.StatusCode, nil
}

func randomLabel() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "chk-" + hex.EncodeToString(b), nil
}
