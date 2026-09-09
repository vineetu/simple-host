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
// domain. A difference in status means the candidate is being served.
//
// Fails closed: if the control cannot be reached the answer is "in use", because
// allowing a claim on the strength of a failed probe is how a live name gets
// taken.
func (p *LiveProbe) InUse(ctx context.Context, domain, name string) (bool, error) {
	control, err := randomLabel()
	if err != nil {
		return true, err
	}
	controlStatus, controlErr := p.status(ctx, control+"."+domain)
	if controlErr != nil {
		return true, controlErr
	}
	for _, host := range []string{name + "." + domain, "sites." + name + "." + domain} {
		status, err := p.status(ctx, host)
		if err != nil {
			// Unreachable is not evidence of use; the control proved the path
			// works, so this is specific to the candidate.
			continue
		}
		if status != controlStatus {
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
