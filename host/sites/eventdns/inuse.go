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
// Listing the zone is not enough. These domains carry a wildcard A record, and
// the web server in front of it routes by name, so a live product can be served
// at <name>.<domain> with no DNS record of its own. Asking the DNS API would
// call that name free, and claiming it would create an explicit record that
// overrides the wildcard and takes a running product off the internet.
//
// So ask the internet instead: fetch the candidate and, in the same breath, a
// name that certainly does not exist. If they answer differently, something is
// there. Comparing against a control means this needs no knowledge of how the
// origin is configured, and keeps working when that changes.
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

// InUse fetches https://<name>.<domain>/ and a control hostname on the same
// domain, then compares what happened.
//
// The comparison is against the control, not against a fixed expectation,
// because what an unused name does varies by domain. On a domain with a
// wildcard and a web server behind it, an unused name returns a 404. On a bare
// domain with nothing attached, an unused name fails to connect at all. Both
// are "nothing is here", and both must let a claim through.
//
// So a failed request is a result, not an error: two failures that match mean
// the domain serves nothing, which is exactly the state a fresh event domain is
// in. Only a candidate that behaves DIFFERENTLY from the control is in use.
func (p *LiveProbe) InUse(ctx context.Context, domain, name string) (bool, error) {
	control, err := randomLabel()
	if err != nil {
		return true, err
	}

	// Each candidate is compared against a control of the SAME SHAPE, because
	// shape alone changes the answer. A wildcard certificate covers one label,
	// so on a domain fronted by one, <name>.<domain> answers while
	// sites.<name>.<domain> cannot connect at all -- whether or not anything is
	// there. Comparing those two against a single control reports every content
	// host as taken, which is a bug I shipped and this is the fix.
	pairs := []struct{ candidate, control string }{
		{name + "." + domain, control + "." + domain},
		{"sites." + name + "." + domain, "sites." + control + "." + domain},
	}
	for _, pair := range pairs {
		controlStatus, controlErr := p.status(ctx, pair.control)
		status, err := p.status(ctx, pair.candidate)
		if (err == nil) != (controlErr == nil) {
			// One answers and its control does not, or the reverse. Something
			// specific to this name is there.
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
