// Package domains connects a person's own domain to one site, and a domain
// for all of an owner's sites (<site>.theirdomain.com). Each needs two DNS
// records: one pointing it here, and a TXT record proving it is theirs. Once
// both are right, the service gets HTTPS certificates for it by itself from
// Let's Encrypt, then Google, then ZeroSSL, and renews them. Certificates
// are written to <data dir>/certs/<host>/{fullchain,privkey}.pem, where
// nginx picks them up by host name with no reload.
package domains

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"simplehost/app"
	"simplehost/core/web"
	sites "simplehost/sites/core"
)

// Mount adds the certificate challenge route on every site address.
func Mount(a *app.App) {
	a.Routes.Site.Handle("GET /.well-known/acme-challenge/{token}", http.HandlerFunc(answerChallenge))
}

// Work checks every domain's DNS and gets or renews its certificates, every
// few minutes, until ctx ends.
func Work(ctx context.Context, a *app.App) {
	for {
		if err := Run(ctx, a); err != nil && ctx.Err() == nil {
			log.Printf("domains: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Minute):
		}
	}
}

// Record is one DNS record to add, and whether DNS shows it now.
type Record struct {
	Type  string `json:"type"`
	Host  string `json:"host"`
	Value string `json:"value"`
	OK    bool   `json:"ok"`
	// Optional is the www or bare partner: it works, redirecting to the
	// domain, once its record is added.
	Optional bool `json:"optional,omitempty"`
}

// Domain is one connected domain and what it still needs.
type Domain struct {
	Host string `json:"host"`
	Kind string `json:"kind"` // "own" (one site) or "shared" (every site of the owner)
	// Status is "waiting" (records missing), "certificate" (records right,
	// HTTPS on its way) or "live".
	Status  string     `json:"status"`
	Says    string     `json:"says"`
	Records []Record   `json:"records"`
	Expires *time.Time `json:"certificate_expires,omitempty"`
	Error   string     `json:"error,omitempty"`
}

var says = map[string]string{
	"waiting":     "Add the DNS records marked missing at your domain's registrar. Changes can take a few minutes to show.",
	"certificate": "The DNS records are right. HTTPS comes up by itself within a few minutes.",
	"live":        "Live over HTTPS.",
}

var hostName = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z][a-z0-9-]{0,61}[a-z0-9]$`)

func clean(host string) (string, error) {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if len(host) > 253 || !hostName.MatchString(host) {
		return "", web.Refuse(http.StatusBadRequest, "bad_domain", "Give a domain name like shop.example.com, without https:// or a path.")
	}
	return host, nil
}

// Connect connects host to the site. The site's own address keeps working.
// A domain someone else has proven is refused; one nobody has proven yet goes
// to whoever proves it first.
func Connect(ctx context.Context, a *app.App, site *sites.Site, host string) (Domain, error) {
	host, err := clean(host)
	if err != nil {
		return Domain{}, err
	}
	return claim(ctx, a, host, "own", site.OwnerID, site.ID)
}

// Share makes <site>.domain answer for every site of the owner (a person or
// team), new ones included, once *.domain points here.
func Share(ctx context.Context, a *app.App, ownerID, domain string) (Domain, error) {
	domain, err := clean(domain)
	if err != nil {
		return Domain{}, err
	}
	return claim(ctx, a, domain, "shared", ownerID, "")
}

func claim(ctx context.Context, a *app.App, host, kind, ownerID, siteID string) (Domain, error) {
	for _, ours := range []string{a.Config.AppDomain, a.Config.SiteDomain} {
		if host == ours || strings.HasSuffix(host, "."+ours) {
			return Domain{}, web.Refuse(http.StatusBadRequest, "our_domain", "That name is one of Simple Host's own. For a free name, use a short name instead.")
		}
	}
	var site any
	if siteID != "" {
		site = siteID
	}
	token := make([]byte, 16)
	rand.Read(token)
	res, err := a.DB.ExecContext(ctx, `INSERT INTO hostnames (host, kind, site_id, owner_id, proof_token) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (host) DO UPDATE SET kind = EXCLUDED.kind, site_id = EXCLUDED.site_id, owner_id = EXCLUDED.owner_id,
			proof_token = EXCLUDED.proof_token, status = 'waiting', cert_status = 'none', error = '', created_at = now()
		WHERE hostnames.proven_at IS NULL AND (hostnames.owner_id, hostnames.kind, hostnames.site_id) IS DISTINCT FROM (EXCLUDED.owner_id, EXCLUDED.kind, EXCLUDED.site_id)`,
		host, kind, site, ownerID, "simple-host-verify="+hex.EncodeToString(token))
	if err != nil {
		return Domain{}, err
	}
	r, err := read(ctx, a, `host = $1`, host)
	if err != nil || len(r) == 0 {
		return Domain{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 && (r[0].ownerID != ownerID || r[0].kind != kind || r[0].siteID.String != siteID) {
		return Domain{}, web.Refuse(http.StatusConflict, "domain_taken", "This domain is connected elsewhere. Remove it there first; if it is not yours, check who can change its DNS.")
	}
	return check(ctx, a, r[0])
}

// Remove disconnects one of the owner's domains. The site's own address
// always keeps working.
func Remove(ctx context.Context, a *app.App, ownerID, host string) error {
	host, err := clean(host)
	if err != nil {
		return err
	}
	var kind string
	err = a.DB.QueryRowContext(ctx, `DELETE FROM hostnames WHERE host = $1 AND owner_id = $2 RETURNING kind`, host, ownerID).Scan(&kind)
	if err == sql.ErrNoRows {
		return web.NotFound
	}
	if err != nil {
		return err
	}
	a.Sites.Forget()
	names := []string{host, sites.Partner(host)}
	if kind == "shared" {
		names, _ = filepath.Glob(filepath.Join(certDir(a), "*."+host))
	}
	for _, n := range names {
		if n != "" {
			os.RemoveAll(filepath.Join(certDir(a), filepath.Base(n)))
		}
	}
	return nil
}

// OfSite is the status of every own domain of a site, checked against DNS now.
func OfSite(ctx context.Context, a *app.App, site *sites.Site) ([]Domain, error) {
	return status(ctx, a, `site_id = $1`, site.ID)
}

// OfOwner is the status of the owner's domain for all sites, checked now.
func OfOwner(ctx context.Context, a *app.App, ownerID string) ([]Domain, error) {
	return status(ctx, a, `owner_id = $1 AND kind = 'shared'`, ownerID)
}

func status(ctx context.Context, a *app.App, where string, arg string) ([]Domain, error) {
	rows, err := read(ctx, a, where, arg)
	out := []Domain{}
	for _, r := range rows {
		d, err := check(ctx, a, r)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, err
}

// row is one hostnames row.
type row struct {
	host, kind, ownerID, prefix, token string
	siteID                             sql.NullString
	proven                             bool
}

func read(ctx context.Context, a *app.App, where string, args ...any) ([]row, error) {
	rs, err := a.DB.QueryContext(ctx, `SELECT host, kind, owner_id, prefix, proof_token, site_id, proven_at IS NOT NULL FROM hostnames WHERE `+where+` ORDER BY host`, args...)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []row
	for rs.Next() {
		var r row
		if err := rs.Scan(&r.host, &r.kind, &r.ownerID, &r.prefix, &r.token, &r.siteID, &r.proven); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rs.Err()
}

// check reads DNS for one domain, marks it proven the first time its TXT
// record is right, and saves and returns its status.
func check(ctx context.Context, a *app.App, r row) (Domain, error) {
	ours, err := resolve(ctx, a, a.Config.AppDomain, "A")
	if err != nil {
		return Domain{}, err
	}
	d := Domain{Host: r.host, Kind: r.kind}
	address := func(name string) Record {
		rec := Record{Type: "CNAME", Host: name, Value: a.Config.AppDomain}
		if !strings.HasPrefix(name, "*.") && sites.Partner(name) != "" && !strings.HasPrefix(name, "www.") && len(ours) > 0 {
			rec = Record{Type: "A", Host: name, Value: ours[0]} // an apex cannot hold a CNAME
		}
		rec.OK = pointsHere(ctx, a, name, ours)
		return rec
	}
	if r.kind == "shared" {
		d.Records = append(d.Records, address("*."+r.host))
	} else {
		d.Records = append(d.Records, address(r.host))
	}
	proven := r.proven
	if r.token != "" {
		proof := Record{Type: "TXT", Host: "_simple-host." + r.host, Value: r.token, OK: r.proven}
		if !proof.OK {
			txt, err := resolve(ctx, a, proof.Host, "TXT")
			proof.OK = err == nil && contains(txt, r.token)
		}
		proven = proof.OK
		d.Records = append(d.Records, proof)
	}
	if p := sites.Partner(r.host); p != "" && r.kind == "own" {
		rec := address(p)
		rec.Optional = true
		d.Records = append(d.Records, rec)
	}
	d.Status = "live"
	for _, rec := range d.Records {
		if !rec.OK && !rec.Optional {
			d.Status = "waiting"
		}
	}
	if d.Status == "live" && r.kind == "own" {
		if exp, ok := certificate(a, r.host); ok {
			d.Expires = &exp
		} else {
			d.Status = "certificate"
		}
	}
	d.Says = says[d.Status]
	var errText, was string
	err = a.DB.QueryRowContext(ctx, `UPDATE hostnames SET status = $2, cert_expires_at = $3,
		proven_at = CASE WHEN $4 THEN COALESCE(proven_at, now()) ELSE proven_at END WHERE host = $1
		RETURNING error, (SELECT status FROM hostnames WHERE host = $1)`,
		r.host, d.Status, d.Expires, proven).Scan(&errText, &was)
	if err == nil && (proven && !r.proven || was != d.Status) {
		a.Sites.Forget() // the address answers, or its main address redirects, differently now
	}
	d.Error = errText
	return d, err
}

// Run is one pass of the worker: check every domain, then get a certificate
// for every name that has none or whose certificate ends within 30 days.
// An own domain's names are it and its partner (when that points here); a
// shared domain's are <site>.domain for each of the owner's sites.
func Run(ctx context.Context, a *app.App) error {
	rows, err := read(ctx, a, `true`)
	if err != nil {
		return err
	}
	for _, r := range rows {
		d, err := check(ctx, a, r)
		if err != nil {
			return err
		}
		if d.Status == "waiting" {
			continue
		}
		var names []string
		for i, rec := range d.Records {
			if r.kind == "own" && rec.Type != "TXT" && rec.OK && (i == 0 || rec.Optional) {
				names = append(names, rec.Host)
			}
		}
		if r.kind == "shared" {
			if names, err = shared(ctx, a, r); err != nil {
				return err
			}
		}
		var failed []string
		for _, name := range names {
			if covered(a, name) {
				continue
			}
			if _, err := issue(ctx, a, name); err != nil {
				failed = append(failed, name+": "+err.Error())
			}
		}
		state, text := "issued", ""
		if failed != nil {
			state, text = "error", "The certificate could not be issued yet; it is tried again in a few minutes. "+strings.Join(failed, "; ")
		}
		if _, err := a.DB.ExecContext(ctx, `UPDATE hostnames SET cert_status = $2, error = $3 WHERE host = $1`, r.host, state, text); err != nil {
			return err
		}
		if r.kind == "own" {
			if _, err := check(ctx, a, r); err != nil {
				return err
			}
		}
	}
	return nil
}

// shared is <label>.domain for every live site of the owner named
// <prefix><label>.
func shared(ctx context.Context, a *app.App, r row) ([]string, error) {
	rs, err := a.DB.QueryContext(ctx, `SELECT name FROM sites WHERE owner_id = $1 AND deleted_at IS NULL AND starts_with(name, $2) AND name <> $2 ORDER BY name`, r.ownerID, r.prefix)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []string
	for rs.Next() {
		var name string
		if err := rs.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, strings.TrimPrefix(name, r.prefix)+"."+r.host)
	}
	return out, rs.Err()
}

// covered: a certificate for name, or for *.<parent> (a carried wildcard),
// lasts more than 30 days yet.
func covered(a *app.App, name string) bool {
	soon := time.Now().Add(30 * 24 * time.Hour)
	_, parent, _ := strings.Cut(name, ".")
	for _, n := range []string{name, "*." + parent} {
		if exp, ok := certificate(a, n); ok && exp.After(soon) {
			return true
		}
	}
	return false
}

// resolve asks the DNS-over-HTTPS resolver for name's records of one type.
func resolve(ctx context.Context, a *app.App, name, typ string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.Config.DNSURL+"?name="+url.QueryEscape(name)+"&type="+typ, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/dns-json")
	res, err := a.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	var out struct {
		Answer []struct {
			Type int    `json:"type"`
			Data string `json:"data"`
		}
	}
	if res.StatusCode != http.StatusOK {
		return nil, errors.New("the DNS resolver answered " + res.Status)
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return nil, err
	}
	want := map[string]int{"A": 1, "CNAME": 5, "TXT": 16}[typ]
	var values []string
	for _, x := range out.Answer {
		if x.Type == want {
			values = append(values, strings.TrimSuffix(strings.Trim(x.Data, `"`), "."))
		}
	}
	return values, nil
}

// pointsHere: name's address is one of the service's (directly, or through
// a CNAME the resolver follows).
func pointsHere(ctx context.Context, a *app.App, name string, ours []string) bool {
	ips, err := resolve(ctx, a, name, "A")
	for _, ip := range ips {
		if err == nil && contains(ours, ip) {
			return true
		}
	}
	return false
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
