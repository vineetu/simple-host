package mcp

import (
	"crypto/aes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

func (c *call) visitorSiteID(site string) (string, error) {
	res := c.do(http.MethodGet, "/v1/sites", nil, nil)
	if !res.ok() {
		return "", restError("visitor reference", res)
	}
	var sites []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if json.Unmarshal(res.body, &sites) != nil {
		return "", errors.New("visitor reference: server returned invalid JSON")
	}
	for _, row := range sites {
		if row.Name == site && row.ID != "" {
			return row.ID, nil
		}
	}
	return "", errors.New("visitor reference: site not found")
}

// Rewrite only server-stamped identity fields, leaving authored data intact.
func (c *call) storageVisitorRefs(site string, response any) error {
	body, _ := response.(map[string]any)
	refs := &siteVisitorRefs{c: c, site: site}
	switch c.policy.visitorResult {
	case "item":
		return refs.stampField(body)
	case "items":
		items, _ := body["items"].([]any)
		for _, item := range items {
			if obj, ok := item.(map[string]any); ok {
				if err := refs.stampField(obj); err != nil {
					return err
				}
			}
		}
	case "rows":
		return refs.stampRows(body)
	}
	return nil
}

// stamp turns a server-stamped account ID into this site's reference. Any
// other value (an owner's own column of that name, say) is authored data,
// returned as it is.
func (r *siteVisitorRefs) stamp(value any) (any, error) {
	id, ok := value.(string)
	if !ok || !looksLikeUUID(id) {
		return value, nil
	}
	return r.encode(id)
}

func (r *siteVisitorRefs) stampField(item map[string]any) error {
	value, ok := item["visitor_id"]
	if !ok {
		return nil
	}
	ref, err := r.stamp(value)
	if err != nil {
		return err
	}
	item["visitor_id"] = ref
	return nil
}

// stampRows stamps every visitor_id column of a SQLite answer.
func (r *siteVisitorRefs) stampRows(body map[string]any) error {
	columns, _ := body["columns"].([]any)
	rows, _ := body["rows"].([]any)
	for i, column := range columns {
		if name, _ := column.(string); !strings.EqualFold(name, "visitor_id") {
			continue
		}
		for _, row := range rows {
			cells, ok := row.([]any)
			if !ok || i >= len(cells) {
				return errors.New("invalid query row")
			}
			ref, err := r.stamp(cells[i])
			if err != nil {
				return err
			}
			cells[i] = ref
		}
	}
	return nil
}

func looksLikeUUID(id string) bool {
	if len(id) != 36 || id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' {
		return false
	}
	_, err := hex.DecodeString(strings.ReplaceAll(id, "-", ""))
	return err == nil
}

// Resolve only well-formed references, using this site's identity. Other SQL
// values retain their meaning, including strings that merely start with v_.
func (c *call) storageVisitorParams(site string, value any) (any, error) {
	params, ok := listArg(value)
	if !ok || params == nil {
		return value, nil
	}
	refs := &siteVisitorRefs{c: c, site: site}
	resolved := append([]any{}, params...)
	for i, param := range params {
		ref, ok := param.(string)
		if !ok || !wellFormedRef(ref) {
			continue
		}
		raw, err := refs.decode(ref)
		if err != nil {
			return nil, err
		}
		resolved[i] = raw
	}
	return resolved, nil
}

// wellFormedRef: ref has the shape of a visitor reference.
func wellFormedRef(ref string) bool {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(ref, "v_"))
	return strings.HasPrefix(ref, "v_") && err == nil && len(raw) == aes.BlockSize
}

// siteVisitorRefs turns raw visitor ids into this site's references and
// back, looking the site's id up once, on first need.
type siteVisitorRefs struct {
	c      *call
	site   string
	siteID string
}

func (r *siteVisitorRefs) id() (string, error) {
	if r.siteID == "" {
		id, err := r.c.visitorSiteID(r.site)
		if err != nil {
			return "", err
		}
		r.siteID = id
	}
	return r.siteID, nil
}

// decode reads a v_ reference; any other id is a raw one, lowercased.
func (r *siteVisitorRefs) decode(id string) (string, error) {
	if !strings.HasPrefix(id, "v_") {
		return strings.ToLower(id), nil
	}
	siteID, err := r.id()
	if err != nil {
		return "", err
	}
	return r.c.server.cfg.VisitorRefs.Decode(siteID, id)
}

func (r *siteVisitorRefs) encode(raw string) (string, error) {
	siteID, err := r.id()
	if err != nil {
		return "", err
	}
	return r.c.server.cfg.VisitorRefs.Encode(siteID, raw)
}
