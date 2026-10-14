package handler

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/vsriram/simple-host/internal/config"
	"github.com/vsriram/simple-host/internal/wpimport"
)

var errImportTooLarge = errors.New("import files too large")

// Paths are validated before ignoring junk and before creating any file.
func importPath(name string) (string, error) {
	name = strings.ReplaceAll(name, `\`, "/")
	if strings.ContainsRune(name, 0) || strings.HasPrefix(name, "/") || (len(name) > 1 && name[1] == ':') {
		return "", errors.New("archive contains an unsafe path")
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return "", errors.New("archive contains a parent path")
		}
	}
	clean := path.Clean(name)
	if clean == "." {
		return "", nil
	}
	return clean, nil
}
func importJunk(name string) bool {
	return strings.HasPrefix(name, "__MACOSX/") || path.Base(name) == ".DS_Store" || path.Base(name) == "Thumbs.db"
}
func importXML(head []byte) bool {
	head = bytes.TrimSpace(bytes.TrimPrefix(bytes.TrimSpace(head), []byte{0xef, 0xbb, 0xbf}))
	return bytes.HasPrefix(head, []byte("<?xml")) || bytes.HasPrefix(head, []byte("<rss"))
}

// SVG and other XML media may begin with an XML declaration too. Only an
// RSS document belongs in the export readers passed to the converter.
func importRSS(head []byte) bool {
	dec := xml.NewDecoder(bytes.NewReader(head))
	for {
		token, err := dec.Token()
		if err != nil {
			return false
		}
		if root, ok := token.(xml.StartElement); ok {
			return root.Name.Local == "rss"
		}
	}
}

type importFiles struct {
	dir             string
	limits          config.Limits
	xml             []string
	media           map[string]wpimport.MediaFile
	entries         int
	total, xmlBytes int64
}

func (f *importFiles) entry(name string, src io.Reader, compressed uint64) error {
	clean, err := importPath(name)
	if err != nil {
		return err
	}
	f.entries++
	if f.entries > f.limits.ImportMaxArchiveEntries {
		return errors.New("archive has too many entries")
	}
	// Anonymous disk names prevent duplicate entries and file/directory collisions.
	dst, err := os.CreateTemp(f.dir, "part-")
	if err != nil {
		return err
	}
	defer dst.Close()
	n, err := io.Copy(dst, io.LimitReader(src, (200<<20)+1))
	if err != nil {
		return errors.New("could not read archive entry")
	}
	if n > 200<<20 {
		return fmt.Errorf("%w: an archive entry is larger than 200 MB", errImportTooLarge)
	}
	f.total += n
	if f.total > int64(f.limits.ImportMaxUnpackedMB)<<20 {
		return fmt.Errorf("%w: unpacked files are too large", errImportTooLarge)
	}
	if compressed > 0 && n > 1<<20 && uint64(n) > compressed*100 {
		return errors.New("archive compression ratio exceeds 100:1")
	}
	if clean == "" || importJunk(clean) {
		return nil
	}
	if _, err = dst.Seek(0, io.SeekStart); err != nil {
		return err
	}
	head, err := io.ReadAll(io.LimitReader(dst, 64<<10))
	if err != nil {
		return err
	}
	if importXML(head) {
		if bytes.Contains(bytes.ToUpper(head), []byte("<!ENTITY")) {
			return errors.New("XML entity declarations are not allowed; export again without custom entities")
		}
	}
	if importXML(head) && importRSS(head) {
		f.xmlBytes += n
		if f.xmlBytes > int64(f.limits.ImportMaxXMLMB)<<20 {
			return fmt.Errorf("%w: WordPress XML files are too large", errImportTooLarge)
		}
		f.xml = append(f.xml, dst.Name())
	} else {
		filename := dst.Name()
		f.media[clean] = wpimport.MediaFile{Size: n, Open: func() (io.ReadCloser, error) { return os.Open(filename) }}
	}
	return nil
}
func (f *importFiles) unpack(filename string) error {
	src, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer src.Close()
	br := bufio.NewReaderSize(src, 64<<10)
	head, _ := br.Peek(64 << 10)
	switch {
	case importXML(head):
		return f.entry(filepath.Base(filename), br, 0)
	case bytes.HasPrefix(head, []byte{'P', 'K', 3, 4}):
		stat, err := src.Stat()
		if err != nil {
			return err
		}
		zr, err := zip.NewReader(src, stat.Size())
		if err != nil {
			return errors.New("invalid ZIP archive")
		}
		for _, e := range zr.File {
			if _, err := importPath(e.Name); err != nil {
				return err
			}
			if e.Mode()&os.ModeType != 0 && !e.FileInfo().IsDir() {
				return errors.New("archive links and devices are not allowed")
			}
			if e.FileInfo().IsDir() {
				f.entries++
				if f.entries > f.limits.ImportMaxArchiveEntries {
					return errors.New("archive has too many entries")
				}
				continue
			}
			r, err := e.Open()
			if err != nil {
				return err
			}
			err = f.entry(e.Name, r, max(e.CompressedSize64, 1))
			r.Close()
			if err != nil {
				return err
			}
		}
		return nil
	case bytes.HasPrefix(head, []byte{0x1f, 0x8b}):
		gz, err := gzip.NewReader(br)
		if err != nil {
			return errors.New("invalid gzip archive")
		}
		defer gz.Close()
		stat, _ := src.Stat()
		if err = f.untar(gz, uint64(stat.Size())); err != nil {
			return err
		}
		n, err := io.Copy(io.Discard, io.LimitReader(gz, (int64(f.limits.ImportMaxUnpackedMB)<<20)+1))
		if n > int64(f.limits.ImportMaxUnpackedMB)<<20 {
			return fmt.Errorf("%w: unpacked files are too large", errImportTooLarge)
		}
		return err
	case len(head) >= 262 && string(head[257:262]) == "ustar":
		return f.untar(br, 0)
	default:
		return errors.New("use a WordPress XML export, ZIP, TAR or TAR.GZ archive")
	}
}
func (f *importFiles) untar(src io.Reader, compressed uint64) error {
	tr := tar.NewReader(src)
	for {
		e, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return errors.New("invalid TAR archive")
		}
		if _, err = importPath(e.Name); err != nil {
			return err
		}
		switch e.Typeflag {
		case tar.TypeDir:
			f.entries++
			if f.entries > f.limits.ImportMaxArchiveEntries {
				return errors.New("archive has too many entries")
			}
		case tar.TypeReg, tar.TypeRegA:
			if err = f.entry(e.Name, tr, compressed); err != nil {
				return err
			}
		default:
			return fmt.Errorf("archive links and devices are not allowed")
		}
	}
}
