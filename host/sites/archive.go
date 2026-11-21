package sites

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"time"
)

// ArchivePut adds one file to an archive being streamed (a .tar.gz or a
// .zip), so one site's layout is written the same way into either.
type ArchivePut func(name string, modTime time.Time, size int64, r io.Reader) error

func TarPut(tw *tar.Writer) ArchivePut {
	return func(name string, modTime time.Time, size int64, r io.Reader) error {
		if err := tw.WriteHeader(&tar.Header{
			Name: name, Mode: 0o644, Size: size, ModTime: modTime, Typeflag: tar.TypeReg,
		}); err != nil {
			return err
		}
		_, err := io.Copy(tw, r)
		return err
	}
}

func ZipPut(zw *zip.Writer) ArchivePut {
	return func(name string, modTime time.Time, _ int64, r io.Reader) error {
		f, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: modTime})
		if err != nil {
			return err
		}
		_, err = io.Copy(f, r)
		return err
	}
}

func PutBytes(put ArchivePut, name string, b []byte) error {
	return put(name, time.Now(), int64(len(b)), bytes.NewReader(b))
}

// WriteArchive streams one site (its files, data and rules, as Export
// yields them while it holds the site lock) into put under prefix.
func (s *Service) WriteArchive(ctx context.Context, put ArchivePut, prefix, siteID string, files FileVersion) error {
	for entry, err := range s.Export(ctx, ExportInput{Prefix: prefix, SiteID: siteID, Files: files}) {
		if err != nil {
			return err
		}
		if err := put(entry.Name, entry.ModTime, entry.Size, entry.Reader); err != nil {
			return err
		}
	}
	return nil
}

// TrashTeamSite moves the holding account's site of that name to Recently
// deleted (its team is gone). Nothing when there is none, or when it is
// taken down (an operator hold keeps it).
func (s *Service) TrashTeamSite(ctx context.Context, accountID, name string) error {
	err := s.Delete(ctx, DeleteInput{UserID: accountID, Name: name})
	var e *Error
	if errors.As(err, &e) && (e.Code == "delete_not_found" || e.Code == "site_suspended") {
		return nil
	}
	return err
}
