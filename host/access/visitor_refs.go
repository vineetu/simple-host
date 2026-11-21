package access

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
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
