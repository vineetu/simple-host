package admin

import (
	"net/http"

	"simplehost/core/web"
	sites "simplehost/sites/core"
)

// The admin page's two lists, every row in one answer so the page sorts and
// filters them itself: GET /v1/admin/sites and GET /v1/admin/people.

// siteList is every site not in a bin, with its sizes, last change, views over
// the last 30 days, owner and addresses.
func (h *handler) siteList(w http.ResponseWriter, r *http.Request) error {
	if !web.CallerOf(r).Operator {
		return errOperator
	}
	list, err := rows(r.Context(), h.a.DB, `SELECT s.id, s.name, s.owner_id, COALESCE(n.name, '') AS owner, p.is_team AS owner_team,
		COALESCE(oa.app_shape, false) AS app_shape, s.created_at, s.changed_at, s.live_version, s.listed, s.offline, s.access,
		s.taken_down_at IS NOT NULL AS taken_down, s.taken_down_reason, p.suspended_at IS NOT NULL AS owner_suspended,
		COALESCE(lv.bytes, 0)::bigint AS live_bytes, COALESCE(av.bytes, 0)::bigint AS bytes, COALESCE(av.n, 0)::bigint AS versions,
		COALESCE(st.views, 0)::bigint AS views_30d, COALESCE(st.people, 0)::bigint AS people_30d,
		COALESCE(hn.host, '') AS domain, COALESCE(hn.status, '') AS domain_status, COALESCE(sn.name, '') AS short_name
		FROM sites s JOIN people p ON p.id = s.owner_id
		LEFT JOIN names n ON n.person_id = s.owner_id AND n.kind = 'handle' AND n.retired_at IS NULL
		LEFT JOIN owner_addresses oa ON oa.owner_id = s.owner_id
		LEFT JOIN versions lv ON lv.site_id = s.id AND lv.n = s.live_version
		LEFT JOIN (SELECT site_id, sum(bytes) AS bytes, count(*) AS n FROM versions GROUP BY site_id) av ON av.site_id = s.id
		LEFT JOIN (SELECT site_id, sum(views) AS views, sum(people) AS people FROM stats_daily WHERE day > current_date - 30 GROUP BY site_id) st ON st.site_id = s.id
		LEFT JOIN LATERAL (SELECT host, status FROM hostnames WHERE site_id = s.id AND kind = 'own' ORDER BY created_at LIMIT 1) hn ON true
		LEFT JOIN LATERAL (SELECT name FROM names WHERE site_id = s.id AND kind = 'short' AND retired_at IS NULL LIMIT 1) sn ON true
		WHERE s.deleted_at IS NULL ORDER BY s.changed_at DESC`)
	if err != nil {
		return err
	}
	for _, s := range list {
		if s["owner"] != "" && s["live_version"].(int64) > 0 {
			s["url"] = h.a.Sites.URL(&sites.Site{Name: s["name"].(string), OwnerHandle: s["owner"].(string), AppShape: s["app_shape"].(bool)})
		}
		delete(s, "app_shape")
	}
	web.JSON(w, http.StatusOK, map[string]any{"sites": list})
	return nil
}

// peopleList is every person (not teams) with their sites, sizes, views over
// the last 30 days and when they last used a browser session or a key.
func (h *handler) peopleList(w http.ResponseWriter, r *http.Request) error {
	if !web.CallerOf(r).Operator {
		return errOperator
	}
	list, err := rows(r.Context(), h.a.DB, `SELECT p.id, p.email, COALESCE(n.name, '') AS handle, p.display_name, p.created_at, p.operator,
		p.suspended_at IS NOT NULL AS suspended, p.suspended_reason,
		COALESCE(sz.sites, 0)::bigint AS sites, COALESCE(sz.bytes, 0)::bigint AS bytes, COALESCE(sz.views, 0)::bigint AS views_30d,
		GREATEST((SELECT max(last_seen_at) FROM sessions WHERE person_id = p.id), (SELECT max(last_used_at) FROM keys WHERE person_id = p.id)) AS last_seen
		FROM people p
		LEFT JOIN names n ON n.person_id = p.id AND n.kind = 'handle' AND n.retired_at IS NULL
		LEFT JOIN (SELECT s.owner_id, count(*) AS sites, sum(COALESCE(v.bytes, 0)) AS bytes,
				sum(COALESCE((SELECT sum(views) FROM stats_daily WHERE site_id = s.id AND day > current_date - 30), 0)) AS views
			FROM sites s LEFT JOIN versions v ON v.site_id = s.id AND v.n = s.live_version
			WHERE s.deleted_at IS NULL GROUP BY s.owner_id) sz ON sz.owner_id = p.id
		WHERE NOT p.is_team ORDER BY p.created_at DESC`)
	if err != nil {
		return err
	}
	web.JSON(w, http.StatusOK, map[string]any{"people": list})
	return nil
}
