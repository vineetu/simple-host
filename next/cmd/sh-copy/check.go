package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	access "simplehost/access/core"
	sites "simplehost/sites/core"
)

// check compares old and new: every copied table row by row, version files
// and namespaces by hash (inside compare), then every address of every live
// site as an anonymous visitor: it must reach the same site, and the visit
// decision (open, passcode, viewers, offline, taken down) must match.
func (r *runner) check(ctx context.Context) error {
	if err := r.compare(ctx); err != nil {
		return err
	}
	live, err := query(ctx, r.old, r.sql(`
		SELECT s.id::text, s.name, s.user_id::text, COALESCE(u.handle, ''),
			CASE WHEN s.domain_verified_at IS NOT NULL THEN lower(s.custom_domain) ELSE '' END,
			CASE WHEN s.suspended_at IS NOT NULL OR u.suspended_at IS NOT NULL THEN 'taken_down' WHEN s.offline_at IS NOT NULL THEN 'offline'
				WHEN s.passcode_enc IS NOT NULL THEN 'passcode' WHEN s.access = 'specific' THEN 'sign_in' ELSE 'open' END
		FROM sites s JOIN users u ON u.id = s.user_id WHERE s.id IN {copied} AND s.deleted_at IS NULL ORDER BY 1`))
	if err != nil {
		return err
	}
	families, err := query(ctx, r.old, `SELECT user_id::text, lower(suffix), site_prefix FROM address_families WHERE verified_at IS NOT NULL`)
	if err != nil {
		return err
	}
	for _, s := range live {
		id, name, owner, handle, domain, gate := s[0].String, s[1].String, s[2].String, s[3].String, s[4].String, s[5].String
		var hosts []string
		if r.appShape(handle) {
			hosts = append(hosts, name+"."+handle+"."+r.cfg.New.AppDomain)
		} else if handle != "" {
			hosts = append(hosts, name+"--"+handle+"."+r.cfg.New.SiteDomain)
		}
		if domain != "" {
			hosts = append(hosts, domain)
		}
		for _, f := range families {
			if label, ok := strings.CutPrefix(name, f[2].String); f[0].String == owner && ok && label != "" {
				hosts = append(hosts, label+"."+f[1].String)
			}
		}
		for _, host := range hosts {
			p, err := r.sites.Lookup(ctx, host)
			if err != nil || p.Site == nil || p.Site.ID != id {
				r.diff("address %s: does not reach site %s (%v)", host, id, err)
				continue
			}
			req, _ := http.NewRequestWithContext(ctx, "GET", "https://"+host+"/", nil)
			d, err := r.sites.Visit(req, p.Site)
			if err != nil || string(d) != gate {
				r.diff("address %s: a visitor gets %q, the old service gives %q (%v)", host, d, gate, err)
			}
		}
	}
	return nil
}

// report lists the owners affected by something not carried: from the old
// database, the old live files, and the notes the copy makes.
func (r *runner) report(ctx context.Context, out io.Writer) (int, error) {
	for _, t := range tables {
		if t.rows != nil {
			if _, err := t.rows(r, ctx, false); err != nil {
				return 0, err
			}
		}
	}
	found, err := query(ctx, r.old, r.sql(`
		SELECT 'older view password', id::text, NULL, 'set a new passcode' FROM sites WHERE id IN {copied} AND view_password_hash IS NOT NULL
		UNION ALL SELECT 'allowed_origins dropped', id::text, NULL, allowed_origins FROM sites WHERE id IN {copied} AND COALESCE(allowed_origins, '') <> ''
		UNION ALL SELECT 'webhook dropped', site_id::text, NULL, url FROM site_notifications WHERE kind = 'webhook' AND state = 'active' AND site_id IN {copied}
		UNION ALL SELECT 'notification choices not carried', site_id::text, NULL, kind || ' ' || storage FROM site_notifications
			WHERE kind IN ('mcp-event', 'web-push') AND state = 'active' AND site_id IN {copied}
		UNION ALL SELECT 'deploy key is now a full key', NULL, user_id::text, COALESCE(name, '') FROM api_keys
			WHERE scope = 'deploy' AND (expires_at IS NULL OR expires_at > now())
		UNION ALL SELECT 'name not carried', site_id::text, person_id::text, name || ' (' || kind || ') loses to another claim on the same name'
			FROM (`+nameCandidates+`) c WHERE pri > (SELECT min(pri) FROM (`+nameCandidates+`) d WHERE d.name = c.name)
		UNION ALL SELECT 'shared domain not carried', NULL, user_id::text, lower(suffix) FROM (SELECT *, row_number() OVER (PARTITION BY lower(suffix)
			ORDER BY verified_at IS NULL, created_at) AS claim FROM address_families) f WHERE claim > 1`))
	if err != nil {
		return 0, err
	}
	for _, x := range found {
		r.notes = append(r.notes, note{x[0].String, x[1].String, x[2].String, x[3].String})
	}
	held, err := query(ctx, r.old, r.sql(`SELECT DISTINCT ON (name) name, kind, site_id::text, person_id::text FROM (`+nameCandidates+`) c
		WHERE retired_at IS NULL ORDER BY name, pri`))
	if err != nil {
		return 0, err
	}
	for _, x := range held {
		if access.Reserved(x[0].String) {
			r.notes = append(r.notes, note{"reserved name carried", x[2].String, x[3].String, x[0].String + " (" + x[1].String + ") is now kept for the service; it was copied as it is"})
		}
	}
	if err := r.scanLive(ctx); err != nil {
		return 0, err
	}
	for _, n := range r.notes {
		var who string
		r.old.QueryRowContext(ctx, `SELECT COALESCE(u.handle, '') || ' <' || u.username || '>' || COALESCE(' site ' || s.name, '')
			FROM users u LEFT JOIN sites s ON s.id::text = $1 WHERE u.id::text = COALESCE(s.user_id::text, $2)`, n.siteID, n.ownerID).Scan(&who)
		fmt.Fprintf(out, "%s\t%s\t%s\n", n.what, who, n.detail)
	}
	return len(r.notes), nil
}

// dropped are addresses old pages may call that the new service does not answer.
var dropped = []string{"/storage/people", "/v1/u/", "/v1/sso/start"}

// scanLive reads every live site's live files in the old data folder: redirect
// lines the new service does not take, and pages calling dropped addresses.
func (r *runner) scanLive(ctx context.Context) error {
	live, err := query(ctx, r.old, r.sql(`SELECT s.id::text, {dir} || '/v' || s.active_version FROM sites s WHERE s.id IN {copied} AND s.deleted_at IS NULL`))
	if err != nil {
		return err
	}
	for _, s := range live {
		root, _ := liveFolder(filepath.Join(r.cfg.OldDir, s[1].String))
		if b, err := os.ReadFile(filepath.Join(root, "_redirects")); err == nil {
			if _, mistakes := sites.ParseRedirects(string(b)); len(mistakes) > 0 {
				r.note("redirect rules not supported", s[0].String, "%s", strings.Join(mistakes, "; "))
			}
		}
		filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.Contains(" .html .htm .js .mjs ", " "+filepath.Ext(p)+" ") {
				return nil
			}
			b, _ := os.ReadFile(p)
			for _, route := range dropped {
				if bytes.Contains(b, []byte(route)) {
					rel, _ := filepath.Rel(root, p)
					r.note("page calls a dropped address", s[0].String, "%s calls %s", rel, route)
				}
			}
			return nil
		})
	}
	return nil
}
