package storage

import (
	"errors"
	"os"
	"path/filepath"
)

// CertRequest describes one issuer hand-off file mutation.
type CertRequest struct {
	Directory, Key, Body string
	Mode                 CertRequestMode
}

// CertRequestMode selects the existing issuer hand-off protocol.
type CertRequestMode uint8

const (
	// CertRequestReplace rewrites a changed request through a .new file.
	CertRequestReplace CertRequestMode = iota
	// CertRequestCreate creates an empty marker only when absent.
	CertRequestCreate
	// CertRequestRemove removes a request if present.
	CertRequestRemove
)

// UpdateCertRequest performs one hand-off mutation and reports marker creation.
func UpdateCertRequest(in CertRequest) (bool, error) {
	p := filepath.Join(in.Directory, "requests", in.Key)
	switch in.Mode {
	case CertRequestCreate:
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			if errors.Is(err, os.ErrExist) {
				return false, nil
			}
			return false, err
		}
		_ = f.Close()
		return true, nil
	case CertRequestRemove:
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return false, err
		}
		return false, nil
	default:
		if cur, err := os.ReadFile(p); err == nil && string(cur) == in.Body {
			return false, nil
		}
		tmp := p + ".new"
		if err := os.WriteFile(tmp, []byte(in.Body), 0o644); err != nil {
			return false, err
		}
		return false, os.Rename(tmp, p)
	}
}
