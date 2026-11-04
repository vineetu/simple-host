package sites

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
	"strconv"
	"strings"

	"github.com/vsriram/simple-host/internal/edition"
	"github.com/vsriram/simple-host/internal/tarball"
	"github.com/vsriram/simple-host/internal/wpimport"
)

var ErrImportTooLarge = errors.New("import files too large")

// Paths are validated before ignoring junk and before creating any file.
func ImportPath(name string) (string, error) {
	if len(name) > tarball.MaxPathLen {
		return "", errors.New("archive path exceeds 1024 bytes")
	}
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

type ImportFiles struct {
	Dir                       string
	Limits                    edition.Limits
	Xml                       []string
	Media                     map[string]wpimport.MediaFile
	entries                   int
	Total, xmlBytes, metadata int64
}

func (f *ImportFiles) Entry(name string, src io.Reader, compressed uint64) error {
	return f.storeEntry(name, src, compressed, false)
}

func (f *ImportFiles) storeEntry(name string, src io.Reader, compressed uint64, streamCounted bool) error {
	clean, err := ImportPath(name)
	if err != nil {
		return err
	}
	if !streamCounted {
		f.entries++
		if f.entries > f.Limits.ImportMaxArchiveEntries {
			return errors.New("archive has too many entries")
		}
	}
	// Anonymous disk names prevent duplicate entries and file/directory collisions.
	dst, err := os.CreateTemp(f.Dir, "part-")
	if err != nil {
		return err
	}
	defer dst.Close()
	n, err := io.Copy(dst, io.LimitReader(src, (200<<20)+1))
	if err != nil {
		return fmt.Errorf("could not read archive entry: %w", err)
	}
	if n > 200<<20 {
		return fmt.Errorf("%w: an archive entry is larger than 200 MB", ErrImportTooLarge)
	}
	if !streamCounted {
		f.Total += n
	}
	if f.Total > int64(f.Limits.ImportMaxUnpackedMB)<<20 {
		return fmt.Errorf("%w: unpacked files are too large", ErrImportTooLarge)
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
		if f.xmlBytes > int64(f.Limits.ImportMaxXMLMB)<<20 {
			return fmt.Errorf("%w: WordPress XML files are too large", ErrImportTooLarge)
		}
		f.Xml = append(f.Xml, dst.Name())
	} else {
		filename := dst.Name()
		f.Media[clean] = wpimport.MediaFile{Size: n, Open: func() (io.ReadCloser, error) { return os.Open(filename) }}
	}
	return nil
}
func (f *ImportFiles) Unpack(filename string) error {
	src, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer src.Close()
	br := bufio.NewReaderSize(src, 64<<10)
	head, _ := br.Peek(64 << 10)
	switch {
	case importXML(head):
		return f.Entry(filepath.Base(filename), br, 0)
	case bytes.HasPrefix(head, []byte{'P', 'K', 3, 4}):
		stat, err := src.Stat()
		if err != nil {
			return err
		}
		section, err := checkImportZIP(src, stat.Size(), f.Limits.ImportMaxArchiveEntries-f.entries)
		if err != nil {
			return err
		}
		zr, err := zip.NewReader(section, section.Size())
		if err != nil {
			return errors.New("invalid ZIP archive")
		}
		for _, e := range zr.File {
			if _, err := ImportPath(e.Name); err != nil {
				return err
			}
			if e.Mode()&os.ModeType != 0 && !e.FileInfo().IsDir() {
				return errors.New("archive links and devices are not allowed")
			}
			if e.FileInfo().IsDir() {
				f.entries++
				if f.entries > f.Limits.ImportMaxArchiveEntries {
					return errors.New("archive has too many entries")
				}
				continue
			}
			r, err := e.Open()
			if err != nil {
				return err
			}
			err = f.Entry(e.Name, r, max(e.CompressedSize64, 1))
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
		return f.untar(gz, uint64(stat.Size()))
	case len(head) >= 262 && string(head[257:262]) == "ustar":
		return f.untar(br, 0)
	default:
		return errors.New("use a WordPress XML export, ZIP, TAR or TAR.GZ archive")
	}
}
func (f *ImportFiles) untar(src io.Reader, compressed uint64) error {
	stream := &importExpandedReader{src: src, files: f, compressed: compressed}
	tr := tar.NewReader(&importTarReader{src: stream, files: f})
	for {
		e, err := tr.Next()
		if err == io.EOF {
			_, err = io.Copy(io.Discard, stream)
			return err
		}
		if err != nil {
			return fmt.Errorf("invalid TAR archive: %w", err)
		}
		if _, err = ImportPath(e.Name); err != nil {
			return err
		}
		switch e.Typeflag {
		case tar.TypeDir:
		case tar.TypeReg, tar.TypeRegA:
			if err = f.storeEntry(e.Name, tr, 0, true); err != nil {
				return err
			}
		default:
			return fmt.Errorf("archive links and devices are not allowed")
		}
	}
}

// Charge every expanded byte, including headers, padding and the trailing drain,
// to the same import-wide budget. Gzip ratios use the entire expanded stream.
type importExpandedReader struct {
	src        io.Reader
	files      *ImportFiles
	compressed uint64
	expanded   int64
}

func (r *importExpandedReader) Read(p []byte) (int, error) {
	remaining := (int64(r.files.Limits.ImportMaxUnpackedMB) << 20) - r.files.Total
	if int64(len(p)) > remaining+1 {
		p = p[:remaining+1]
	}
	n, err := r.src.Read(p)
	r.files.Total += int64(n)
	r.expanded += int64(n)
	if r.files.Total > int64(r.files.Limits.ImportMaxUnpackedMB)<<20 {
		return n, ErrImportTooLarge
	}
	if r.compressed > 0 && r.expanded > 10<<20 && uint64(r.expanded) > r.compressed*100 {
		return n, errors.New("archive compression ratio exceeds 100:1")
	}
	return n, err
}

// Inspect physical headers before archive/tar can hide or allocate PAX/GNU
// metadata. Forward at most one header or body span per Read.
type importTarReader struct {
	src       io.Reader
	files     *ImportFiles
	remaining int64
	ended     bool
	header    [512]byte
	offset    int
	pending   bool
	special   io.Reader
}

func (r *importTarReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if r.pending {
		return r.readHeader(p)
	}
	if r.remaining > 0 {
		if int64(len(p)) > r.remaining {
			p = p[:r.remaining]
		}
		src := r.src
		if r.special != nil {
			src = r.special
		}
		n, err := src.Read(p)
		r.remaining -= int64(n)
		return n, err
	}
	if r.ended {
		return r.src.Read(p)
	}
	r.special = nil
	var header [512]byte
	if _, err := io.ReadFull(r.src, header[:]); err != nil {
		return 0, err
	}
	if header == [512]byte{} {
		r.ended = true
	} else {
		r.files.entries++
		if r.files.entries > r.files.Limits.ImportMaxArchiveEntries {
			return 0, errors.New("archive has too many physical entries")
		}
		size, err := strconv.ParseInt(strings.Trim(string(header[124:136]), " \x00"), 8, 64)
		if err != nil || size < 0 || size > (int64(r.files.Limits.ImportMaxUnpackedMB)<<20) {
			return 0, errors.New("invalid or oversized TAR entry")
		}
		switch header[156] {
		case tar.TypeXHeader, tar.TypeXGlobalHeader, tar.TypeGNULongName, tar.TypeGNULongLink:
			r.files.metadata += size
			if r.files.metadata > 1<<20 {
				return 0, errors.New("TAR extended headers exceed 1 MB")
			}
		}
		r.remaining = ((size + 511) / 512) * 512
		if header[156] == tar.TypeGNUSparse {
			return 0, errors.New("sparse TAR entries are not allowed")
		}
		if header[156] == tar.TypeXHeader || header[156] == tar.TypeXGlobalHeader {
			// PAX size/sparse overrides can change the boundaries archive/tar
			// sees and hide physical headers from this scanner. WordPress
			// archives do not need these overrides; refuse them before parsing.
			body := make([]byte, r.remaining)
			if _, err := io.ReadFull(r.src, body); err != nil {
				return 0, err
			}
			if err := checkImportPAX(body[:size]); err != nil {
				return 0, err
			}
			r.special = bytes.NewReader(body)
		}
	}
	// tar.Reader requests complete 512-byte headers. Buffer smaller reads too.
	r.header = header
	r.offset = 0
	r.pending = true
	return r.readHeader(p)
}

func (r *importTarReader) readHeader(p []byte) (int, error) {
	n := copy(p, r.header[r.offset:])
	r.offset += n
	r.pending = r.offset < len(r.header)
	return n, nil
}

func checkImportPAX(body []byte) error {
	for len(body) > 0 {
		space := bytes.IndexByte(body, ' ')
		if space < 1 {
			return errors.New("invalid PAX record")
		}
		n, err := strconv.Atoi(string(body[:space]))
		if err != nil || n <= space+1 || n > len(body) || body[n-1] != '\n' {
			return errors.New("invalid PAX record")
		}
		record := body[space+1 : n-1]
		if bytes.HasPrefix(record, []byte("size=")) || bytes.HasPrefix(record, []byte("GNU.sparse.")) {
			return errors.New("TAR size or sparse overrides are not allowed")
		}
		body = body[n:]
	}
	return nil
}
