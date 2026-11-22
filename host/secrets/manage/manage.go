package manage

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/secrets"
	"github.com/vsriram/simple-host/host/sites"
)

// Refusal is a request this feature turns down; Code maps to its status
// through httpx.RefusalStatus.
type Refusal struct{ Code, Message string }

func (e *Refusal) Error() string { return e.Message }

// MaxValueBytes is the longest value a secret may hold.
const MaxValueBytes = 8192

var (
	namePattern  = regexp.MustCompile(`^[a-z0-9_-]{1,64}$`)
	labelPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	// privateSuffixes never name a host on the public internet.
	privateSuffixes = []string{"localhost", "local", "internal", "intranet", "lan", "home", "corp", "private", "arpa", "test", "invalid", "example", "onion"}
	whoValues       = map[string]bool{"owner": true, "signed-in": true, "anyone": true}
	// attachPatterns: the shape of a header name or a query parameter name.
	attachPatterns = map[string]*regexp.Regexp{
		"header": regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`),
		"query":  regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`),
	}
	prefixPattern = regexp.MustCompile(`^[\x20-\x7e]{0,32}$`)
	// neverAttached are headers that carry the request itself, never a key.
	neverAttached = map[string]bool{"host": true, "cookie": true, "content-length": true, "content-type": true, "content-encoding": true,
		"transfer-encoding": true, "connection": true, "keep-alive": true, "te": true, "trailer": true, "upgrade": true, "accept": true}
)

// Save stores value as the account's site's secret v.Name with v's
// settings, replacing one of that name. who and attach are required: who may use the key is the owner's own
// choice, never a default (owner decision, 2026-11-22).
func Save(ctx context.Context, db *sql.DB, hosts *sites.Hosts, store *secrets.Store, accountID, site, value string, v secrets.Secret) error {
	v.Destination = strings.ToLower(strings.TrimSpace(v.Destination))
	name, who := v.Name, v.Who
	switch {
	case !namePattern.MatchString(name):
		return &Refusal{"invalid_secret_name", "a secret's name is 1 to 64 lowercase letters, digits, - and _"}
	case len(value) == 0 || len(value) > MaxValueBytes || strings.ContainsFunc(value, unicode.IsControl):
		return &Refusal{"invalid_secret_value", "the key must be 1 to 8192 bytes, on one line"}
	case !publicHost(v.Destination):
		return &Refusal{"invalid_destination", "destination is the one outside host the key may be sent to, as a bare name such as api.example.com: no https://, path, port, IP address or private name"}
	case !whoValues[who]:
		return &Refusal{"invalid_who", "choose who may use this key: owner (only you), signed-in (people signed in on the site) or anyone (any visitor of the site)"}
	case attachPatterns[v.Attach] == nil || !attachPatterns[v.Attach].MatchString(v.AttachName) || v.Attach == "header" && neverAttached[strings.ToLower(v.AttachName)]:
		return &Refusal{"invalid_attach", "say how the key goes with each request: attach header (with attach_name such as Authorization or x-api-key) or query (with attach_name such as key)"}
	case !prefixPattern.MatchString(v.AttachPrefix):
		return &Refusal{"invalid_attach_prefix", "attach_prefix is at most 32 plain characters, such as \"Bearer \""}
	}
	if hosts.OwnHost(ctx, v.Destination) {
		return &Refusal{"invalid_destination", "destination is one of this service's own addresses: a key may only go to an outside service"}
	}
	siteID, err := writableSite(ctx, db, accountID, site)
	if err != nil {
		return err
	}
	if err := store.Save(ctx, siteID, value, v); errors.Is(err, secrets.ErrTooMany) {
		return &Refusal{"too_many_secrets", "a site holds at most 20 secrets: delete one first"}
	} else if err != nil {
		return err
	}
	return nil
}

// List is the account's site's secrets, without their values.
func List(ctx context.Context, db *sql.DB, store *secrets.Store, accountID, site string) ([]secrets.Secret, error) {
	siteID, err := siteID(ctx, db, accountID, site)
	if err != nil {
		return nil, err
	}
	return store.List(ctx, siteID)
}

// Delete removes the account's site's secret name.
func Delete(ctx context.Context, db *sql.DB, store *secrets.Store, accountID, site, name string) error {
	siteID, err := writableSite(ctx, db, accountID, site)
	if err != nil {
		return err
	}
	removed, err := store.Delete(ctx, siteID, name)
	if err == nil && !removed {
		return &Refusal{"secret_not_found", "this site has no secret of that name"}
	}
	return err
}

func siteID(ctx context.Context, db *sql.DB, accountID, name string) (string, error) {
	site, err := siteNamed(ctx, db, accountID, name)
	return site.ID, err
}

// writableSite is siteID for a change: a site the operator took down (or
// whose owner is suspended) refuses changes, as every site write does.
func writableSite(ctx context.Context, db *sql.DB, accountID, name string) (string, error) {
	site, err := siteNamed(ctx, db, accountID, name)
	if err == nil && site.Suspended() {
		return "", &Refusal{"site_suspended", site.SuspendedReason()}
	}
	return site.ID, err
}

func siteNamed(ctx context.Context, db *sql.DB, accountID, name string) (model.Site, error) {
	site, err := sites.SiteByName(ctx, db, accountID, name)
	if errors.Is(err, sql.ErrNoRows) {
		return site, &Refusal{"site_not_found", "no site of that name"}
	}
	return site, err
}

// publicHost reports whether host is a bare public host name: at least two
// labels, no scheme, path, port or IP literal, and not a private name.
func publicHost(host string) bool {
	if len(host) > 253 || net.ParseIP(host) != nil {
		return false
	}
	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return false
	}
	for _, l := range labels {
		if !labelPattern.MatchString(l) {
			return false
		}
	}
	last := labels[len(labels)-1]
	if strings.Trim(last, "0123456789") == "" {
		return false
	}
	return !slices.Contains(privateSuffixes, last)
}
