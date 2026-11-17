// Package static holds the optional page server's routing copy. It is not
// wired into either program until the Static process slice.
package static

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/model"
)

const snapshotVersion = 1
const snapshotMaxAge = 7 * 24 * time.Hour
const snapshotName = "routing.json"

type snapshotHeader struct {
	Version   int       `json:"version"`
	InstallID string    `json:"install_id"`
	Product   string    `json:"product"`
	LoadedAt  time.Time `json:"loaded_at"`
	Checksum  string    `json:"checksum"`
}
type savedSnapshot struct {
	Header snapshotHeader  `json:"header"`
	Body   json.RawMessage `json:"body"`
}
type Snapshot struct {
	Table    model.SiteTable
	LoadedAt time.Time
}

// readIdentity never creates or repairs the rules track's identity file.
func readIdentity(dataDir string) (string, error) {
	b, err := os.ReadFile(filepath.Join(dataDir, ".install-id"))
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(b))
	if id == "" {
		return "", errors.New("install identity is empty")
	}
	return id, nil
}
func checksum(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }

func loadSnapshot(stateDir, dataDir, product string, now time.Time) (*Snapshot, error) {
	identity, err := readIdentity(dataDir)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(filepath.Join(stateDir, snapshotName))
	if err != nil {
		return nil, err
	}
	var saved savedSnapshot
	if err = json.Unmarshal(b, &saved); err != nil {
		return nil, fmt.Errorf("invalid routing snapshot: %w", err)
	}
	if saved.Header.Version != snapshotVersion {
		return nil, errors.New("unknown routing snapshot version")
	}
	if saved.Header.InstallID != identity || saved.Header.Product != product {
		return nil, errors.New("routing snapshot belongs to another install or product")
	}
	if saved.Header.LoadedAt.IsZero() || now.Sub(saved.Header.LoadedAt) > snapshotMaxAge {
		return nil, errors.New("routing snapshot is older than seven days")
	}
	if checksum(saved.Body) != saved.Header.Checksum {
		return nil, errors.New("routing snapshot checksum mismatch")
	}
	var table model.SiteTable
	if err = json.Unmarshal(saved.Body, &table); err != nil {
		return nil, fmt.Errorf("invalid routing table: %w", err)
	}
	return &Snapshot{Table: table, LoadedAt: saved.Header.LoadedAt}, nil
}

func saveSnapshot(stateDir, dataDir, product string, snapshot *Snapshot, beforeRename func() error) error {
	identity, err := readIdentity(dataDir)
	if err != nil {
		return err
	}
	body, err := json.Marshal(snapshot.Table)
	if err != nil {
		return err
	}
	saved := savedSnapshot{Header: snapshotHeader{snapshotVersion, identity, product, snapshot.LoadedAt, checksum(body)}, Body: body}
	data, err := json.Marshal(saved)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(stateDir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(stateDir, ".routing-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	defer f.Close()
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if beforeRename != nil {
		if err = beforeRename(); err != nil {
			return err
		}
	}
	if err = os.Rename(name, filepath.Join(stateDir, snapshotName)); err != nil {
		return err
	}
	dir, err := os.Open(stateDir)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
