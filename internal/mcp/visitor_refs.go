package mcp

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

// VisitorReferences maps UUIDs to opaque references using a separate AES key
// per site. The key comes from PASSCODE_ENC_KEY, so references survive restarts.
// No mapping or account identifier is stored in the reference.
type VisitorReferences struct{ key []byte }

func NewVisitorReferences(key []byte) *VisitorReferences {
	return &VisitorReferences{key: append([]byte(nil), key...)}
}

func (v *VisitorReferences) block(siteID string) (cipher.Block, error) {
	if v == nil || len(v.key) == 0 || siteID == "" {
		return nil, errors.New("visitor references unavailable")
	}
	mac := hmac.New(sha256.New, v.key)
	mac.Write([]byte("visitor-ref:" + siteID))
	return aes.NewCipher(mac.Sum(nil))
}

func (v *VisitorReferences) Encode(siteID, id string) (string, error) {
	if id == "" {
		return "", nil
	}
	if len(id) != 36 || id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' {
		return "", errors.New("invalid visitor ID")
	}
	raw, err := hex.DecodeString(strings.ReplaceAll(id, "-", ""))
	if err != nil || len(raw) != aes.BlockSize {
		return "", errors.New("invalid visitor ID")
	}
	block, err := v.block(siteID)
	if err != nil {
		return "", err
	}
	block.Encrypt(raw, raw)
	return "v_" + base64.RawURLEncoding.EncodeToString(raw), nil
}

func (v *VisitorReferences) Decode(siteID, ref string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(ref, "v_"))
	if !strings.HasPrefix(ref, "v_") || err != nil || len(raw) != aes.BlockSize {
		return "", errors.New("invalid visitor reference")
	}
	block, err := v.block(siteID)
	if err != nil {
		return "", err
	}
	block.Decrypt(raw, raw)
	id := hex.EncodeToString(raw)
	return id[:8] + "-" + id[8:12] + "-" + id[12:16] + "-" + id[16:20] + "-" + id[20:], nil
}

// Resolve the internal site ID through the same authenticated REST boundary.
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
func (c *call) storageVisitorRefs(site, tool string, response any) error {
	body, _ := response.(map[string]any)
	siteID := ""
	convert := func(value any) (any, error) {
		if value == nil || value == "" {
			return value, nil
		}
		id, ok := value.(string)
		if !ok || !looksLikeUUID(id) {
			// Not a server-stamped account ID (an owner's own column of
			// that name, say): authored data, returned as it is.
			return value, nil
		}
		if siteID == "" {
			var err error
			siteID, err = c.visitorSiteID(site)
			if err != nil {
				return nil, err
			}
		}
		return c.server.cfg.VisitorRefs.Encode(siteID, id)
	}
	field := func(item map[string]any) error {
		if value, ok := item["visitor_id"]; ok {
			ref, err := convert(value)
			if err != nil {
				return err
			}
			item["visitor_id"] = ref
		}
		return nil
	}
	switch tool {
	case "storage_get_kv":
		return field(body)
	case "storage_list_kv_keys", "storage_list_file_objects":
		items, _ := body["items"].([]any)
		for _, item := range items {
			if obj, ok := item.(map[string]any); ok {
				if err := field(obj); err != nil {
					return err
				}
			}
		}
	case "storage_sql_query":
		columns, _ := body["columns"].([]any)
		rows, _ := body["rows"].([]any)
		for i, column := range columns {
			name, _ := column.(string)
			if !strings.EqualFold(name, "visitor_id") {
				continue
			}
			for _, row := range rows {
				cells, ok := row.([]any)
				if !ok || i >= len(cells) {
					return errors.New("invalid query row")
				}
				ref, err := convert(cells[i])
				if err != nil {
					return err
				}
				cells[i] = ref
			}
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
	resolved := append([]any{}, params...)
	siteID := ""
	for i, param := range params {
		ref, ok := param.(string)
		if !ok || !strings.HasPrefix(ref, "v_") {
			continue
		}
		raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(ref, "v_"))
		if err != nil || len(raw) != aes.BlockSize {
			continue
		}
		if siteID == "" {
			siteID, err = c.visitorSiteID(site)
			if err != nil {
				return nil, err
			}
		}
		resolved[i], err = c.server.cfg.VisitorRefs.Decode(siteID, ref)
		if err != nil {
			return nil, err
		}
	}
	return resolved, nil
}
