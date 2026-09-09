// Package eventdns hands an event organiser two hostnames under a domain we
// control, pointing at their own server.
//
// It exists because the alternative is asking a non-technical organiser to buy
// a domain and add DNS records before their event can start, which is the step
// people give up on. We create the records; they never touch a registrar.
package eventdns

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Provider is the DNS API. Only Vercel is implemented; the interface exists so
// the handler does not import an HTTP client.
type Provider interface {
	CreateA(ctx context.Context, domain, name, ip string, ttl int) (recordID string, err error)
	DeleteRecord(ctx context.Context, domain, recordID string) error
	// TakenNames returns every label that already has a record on the zone, so a
	// claim cannot override a name that is already serving something.
	TakenNames(ctx context.Context, domain string) (map[string]bool, error)
}

// Vercel talks to the Vercel DNS API. A team-owned domain requires teamId on
// every call; without it the API answers 403 and the failure looks like a bad
// token rather than a missing parameter.
type Vercel struct {
	Token  string
	TeamID string
	HTTP   *http.Client
}

func NewVercel(token, teamID string) *Vercel {
	return &Vercel{Token: token, TeamID: teamID, HTTP: &http.Client{Timeout: 20 * time.Second}}
}

func (v *Vercel) q() string {
	if v.TeamID == "" {
		return ""
	}
	return "?teamId=" + url.QueryEscape(v.TeamID)
}

func (v *Vercel) CreateA(ctx context.Context, domain, name, ip string, ttl int) (string, error) {
	body, _ := json.Marshal(map[string]any{"name": name, "type": "A", "value": ip, "ttl": ttl})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.vercel.com/v2/domains/"+url.PathEscape(domain)+"/records"+v.q(), bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+v.Token)
	req.Header.Set("Content-Type", "application/json")
	res, err := v.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	var out struct {
		UID   string `json:"uid"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.NewDecoder(res.Body).Decode(&out)
	if res.StatusCode >= 300 || out.UID == "" {
		msg := out.Error.Message
		if msg == "" {
			msg = res.Status
		}
		return "", fmt.Errorf("create record: %s", msg)
	}
	return out.UID, nil
}

func (v *Vercel) DeleteRecord(ctx context.Context, domain, recordID string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		"https://api.vercel.com/v2/domains/"+url.PathEscape(domain)+"/records/"+url.PathEscape(recordID)+v.q(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+v.Token)
	res, err := v.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	// A record already gone is the desired state, not a failure: teardown has to
	// be safe to retry after a partial run.
	if res.StatusCode >= 300 && res.StatusCode != http.StatusNotFound {
		return fmt.Errorf("delete record: %s", res.Status)
	}
	return nil
}

var nameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$`)

// reserved names would collide with the instance's own hostnames or with
// something a browser or a certificate authority treats specially.
var reserved = map[string]bool{
	"www": true, "sites": true, "cname": true, "api": true, "admin": true,
	"mail": true, "smtp": true, "imap": true, "ns1": true, "ns2": true,
	"lab": true, "d": true, "console": true, "usage": true, "auth": true,
	"_dmarc": true, "_domainkey": true, "localhost": true,
}

// NormalizeName lowercases and trims an event name, then rejects anything that
// cannot safely become <name>.<domain> and sites.<name>.<domain>.
//
// It returns the canonical form rather than only an error, so callers cannot
// validate one string and then store a different one. DNS labels are
// case-insensitive, so an organiser typing "Stanford-CS" gets "stanford-cs"
// rather than a complaint about capitals.
func NormalizeName(name string) (string, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if !nameRe.MatchString(name) {
		return "", fmt.Errorf("event name must be 1 to 40 letters, digits or hyphens, and cannot start or end with a hyphen")
	}
	if reserved[name] {
		return "", fmt.Errorf("event name %q is reserved", name)
	}
	return name, nil
}

// ValidatePublicIP rejects anything that is not a routable IPv4 address.
//
// A private or loopback address here would create a public record pointing
// inside someone's network, and certificate issuance would fail in a way that
// looks like our bug rather than their typo.
// ValidatePublicIP returns the canonical dotted-quad form, so a caller cannot
// validate one string and then send a different one to the DNS API. "::ffff:8.8.8.8"
// is a valid IPv4 address that a DNS provider will reject as an A record value.
func ValidatePublicIP(ip string) (string, error) {
	parsed := net.ParseIP(strings.TrimSpace(ip))
	if parsed == nil || parsed.To4() == nil {
		return "", fmt.Errorf("ip must be a public IPv4 address")
	}
	if parsed.IsLoopback() || parsed.IsPrivate() || parsed.IsUnspecified() ||
		parsed.IsLinkLocalUnicast() || parsed.IsMulticast() {
		return "", fmt.Errorf("ip must be a public address, not %s", parsed)
	}
	// Ranges that are neither private nor routable on the internet. Go's
	// IsPrivate covers only RFC 1918, so these have to be listed. A record
	// pointing at one can never work, and refusing it now is a clearer error
	// than a certificate failure twenty minutes later.
	for _, r := range nonRoutable {
		if r.Contains(parsed) {
			return "", fmt.Errorf("ip must be a public address, not %s (%s is not routable)", parsed, r)
		}
	}
	return parsed.To4().String(), nil
}

// nonRoutable is parsed once at startup; a malformed entry here is a programming
// error, so MustParseCIDR-style panic behaviour is what we want.
var nonRoutable = func() []*net.IPNet {
	var out []*net.IPNet
	for _, c := range []string{
		"0.0.0.0/8",       // "this network"
		"100.64.0.0/10",   // carrier-grade NAT
		"192.0.0.0/24",    // IETF protocol assignments
		"192.0.2.0/24",    // documentation
		"198.18.0.0/15",   // benchmarking
		"198.51.100.0/24", // documentation
		"203.0.113.0/24",  // documentation
		"240.0.0.0/4",     // reserved
	} {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			panic("eventdns: bad CIDR " + c)
		}
		out = append(out, n)
	}
	return out
}()

// TakenNames lists every label that already holds a record on the zone.
//
// A static reserved list cannot know what the zone actually serves. These
// domains carry a wildcard, so a live product at sf-gog.example or
// jellyfin.example is answered by that wildcard with no explicit record of its
// own -- until someone claims that exact name, whose explicit record overrides
// the wildcard. They could then obtain a certificate and serve content on a
// hostname belonging to a real product. Asking the zone closes that.
func (v *Vercel) TakenNames(ctx context.Context, domain string) (map[string]bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.vercel.com/v4/domains/"+url.PathEscape(domain)+"/records"+v.q(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+v.Token)
	res, err := v.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return nil, fmt.Errorf("list records: %s", res.Status)
	}
	var out struct {
		Records []struct {
			Name string `json:"name"`
		} `json:"records"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return nil, err
	}
	taken := map[string]bool{}
	for _, r := range out.Records {
		n := strings.ToLower(strings.TrimSpace(r.Name))
		if n == "" || n == "@" {
			continue
		}
		// A record at sites.foo means the label foo is in use for our purposes,
		// because that is exactly the pair a claim would create.
		taken[n] = true
		if _, rest, found := strings.Cut(n, "."); found && rest != "" {
			taken[rest] = true
		}
	}
	return taken, nil
}
