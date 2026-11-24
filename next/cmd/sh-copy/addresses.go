package main

import (
	"context"
	"crypto/x509"
	"database/sql"
	"encoding/pem"
	"os"
	"path/filepath"
	"time"

	sites "simplehost/sites/core"
)

// appShape: the account has its own *.<handle>.<app domain> certificate on
// the old service (a marker named by its handle), so it keeps the two-label
// addresses. Everyone else already answers at <site>--<handle>.<site domain>.
func (r *runner) appShape(handle string) bool {
	_, err := os.Stat(filepath.Join(r.cfg.OldCertReady, handle))
	return handle != "" && err == nil
}

// addresses: every account with a handle, its home site and address shape.
func (r *runner) addresses(ctx context.Context, write bool) ([]row, error) {
	old, err := query(ctx, r.old, r.sql(`SELECT id::text, CASE WHEN home_site_id IN {copied} THEN home_site_id::text END, handle
		FROM users WHERE handle IS NOT NULL ORDER BY 1`))
	for i, x := range old {
		old[i] = row{x[0], x[1], text(map[bool]string{true: "true", false: "false"}[r.appShape(x[2].String)])}
	}
	return old, err
}

// hostnames: own domains (one site) and shared domains (every site of an
// owner), in the words sites/domains uses. A domain live on the old service
// with its certificate on disk comes across live, the certificate copied to
// certs/<host>/ (and its www or bare partner when it covers that too), or to
// certs/*.<domain>/ for a shared domain's wildcard.
func (r *runner) hostnames(ctx context.Context, write bool) ([]row, error) {
	old, err := query(ctx, r.old, r.sql(`
		SELECT lower(s.custom_domain), 'own', s.id::text, s.user_id::text, '', COALESCE(s.domain_token, ''), s.domain_verified_at::text,
			COALESCE(s.domain_cert_status, ''), COALESCE(s.domain_last_error, ''), COALESCE(s.domain_bound_at, s.created_at, {never})::text, lower(s.custom_domain)
			FROM sites s WHERE s.id IN {copied} AND s.custom_domain IS NOT NULL AND NOT (`+isShort+`)
		UNION ALL SELECT * FROM (SELECT DISTINCT ON (lower(suffix)) lower(suffix), 'shared', NULL, user_id::text, site_prefix, token, verified_at::text,
			'', COALESCE(last_error, ''), created_at::text, COALESCE(cert_name, lower(suffix)) FROM address_families
			ORDER BY lower(suffix), verified_at IS NULL, created_at) f ORDER BY 1`))
	if err != nil {
		return nil, err
	}
	var out []row
	for _, x := range old {
		host, kind, proven := x[0].String, x[1].String, x[6].Valid
		status, certStatus, expires := "waiting", "none", sql.NullString{}
		names := []string{"*." + host}
		if kind == "own" {
			names = []string{host}
			if p := sites.Partner(host); p != "" {
				names = append(names, p)
			}
		}
		exp, ok, err := r.certificate(filepath.Join(r.cfg.OldCertDir, x[10].String), names, write)
		if err != nil {
			return nil, err
		}
		switch {
		case proven && kind == "shared":
			status = "live"
		case proven && ok:
			status, expires = "live", text(exp.UTC().Format("2006-01-02 15:04:05")+"+00")
		case proven:
			status = "certificate"
		}
		if ok {
			certStatus = "issued"
		} else if x[7].String == "failed" {
			certStatus = "error"
		}
		out = append(out, row{x[0], x[1], x[2], x[3], x[4], x[5], x[6], text(status), text(certStatus), expires, x[8], x[9]})
	}
	return out, nil
}

// certificate copies a carried certificate (fullchain.pem, privkey.pem) to
// certs/<name>/ for each name it covers, when it covers the first name and
// has not ended. Without write it only reports copies that differ.
func (r *runner) certificate(lineage string, names []string, write bool) (time.Time, bool, error) {
	b, err := os.ReadFile(filepath.Join(lineage, "fullchain.pem"))
	if err != nil {
		return time.Time{}, false, nil
	}
	block, _ := pem.Decode(b)
	if block == nil {
		return time.Time{}, false, nil
	}
	c, err := x509.ParseCertificate(block.Bytes)
	probe := func(n string) string { // a wildcard is checked with a label in its place
		if len(n) > 2 && n[:2] == "*." {
			return "x" + n[1:]
		}
		return n
	}
	if err != nil || time.Now().After(c.NotAfter) || c.VerifyHostname(probe(names[0])) != nil {
		return time.Time{}, false, nil
	}
	for _, n := range names {
		if c.VerifyHostname(probe(n)) != nil {
			continue
		}
		for _, f := range []string{"fullchain.pem", "privkey.pem"} {
			src, dst := filepath.Join(lineage, f), filepath.Join(r.cfg.New.DataDir, "certs", n, f)
			if same(src, dst) {
				continue
			}
			if !write {
				r.diff("certificate %s: missing or different in the new data folder", dst)
				continue
			}
			r.changes++
			if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
				return c.NotAfter, true, err
			}
			if err := copyFile(src, dst); err != nil {
				return c.NotAfter, true, err
			}
			os.Chmod(dst, 0o600)
		}
	}
	return c.NotAfter, true, nil
}
