package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// keyVersion is the master key a row was sealed with; there is one so far.
const keyVersion = 1

// masterKey reads the 32-byte master key file, which no other user may read.
// When create is set (no secret is saved yet) and the file is absent it is
// made first, with 32 random bytes and mode 0600; two first saves at once
// both end up reading the one file that won. Absent while secrets exist, it
// is never made again: a new key would split the store.
func masterKey(path string, create bool) ([]byte, error) {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) && create {
		if err = writeNewKey(path); err == nil || errors.Is(err, fs.ErrExist) {
			info, err = os.Stat(path)
		}
	}
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("secrets: master key file %s is missing while saved secrets exist: restore it from its backup (a new one would not open them)", path)
	}
	if err != nil {
		return nil, fmt.Errorf("secrets: master key file %s: %w", path, err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("secrets: master key file %s is readable by other users (mode %o): chmod 600 it", path, info.Mode().Perm())
	}
	key, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("secrets: master key file %s: %w", path, err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("secrets: master key file %s holds %d bytes, not 32", path, len(key))
	}
	return key, nil
}

func writeNewKey(path string) error {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(key); err != nil {
		f.Close()
		os.Remove(path)
		return err
	}
	return f.Close()
}

// aead is AES-256-GCM under the master key at path.
func aead(path string, create bool) (cipher.AEAD, error) {
	key, err := masterKey(path, create)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// boundTo is the additional data a value is sealed with: its site, its name
// and where and how it is sent, so a ciphertext moved to another site or
// name, or a row whose destination or attach was edited, does not open.
func boundTo(siteID string, v Secret) []byte {
	return []byte(strings.Join([]string{siteID, v.Name, v.Destination, v.Attach, v.AttachName, v.AttachPrefix}, "\x00"))
}

func seal(path string, create bool, siteID string, v Secret, value string) (ciphertext, nonce []byte, err error) {
	gcm, err := aead(path, create)
	if err != nil {
		return nil, nil, err
	}
	nonce = make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	return gcm.Seal(nil, nonce, []byte(value), boundTo(siteID, v)), nonce, nil
}

func unseal(path, siteID string, v Secret, ciphertext, nonce []byte) (string, error) {
	gcm, err := aead(path, false)
	if err != nil {
		return "", err
	}
	if len(nonce) != gcm.NonceSize() {
		return "", errors.New("secrets: a saved secret's nonce is damaged")
	}
	plain, err := gcm.Open(nil, nonce, ciphertext, boundTo(siteID, v))
	if err != nil {
		return "", errors.New("secrets: a saved secret does not open under the master key file")
	}
	return string(plain), nil
}
