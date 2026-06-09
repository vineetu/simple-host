package tarball

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

const maxTotalUncompressedSize = 500 * 1024 * 1024

func Extract(r io.Reader, filename string) (map[string][]byte, error) {
	switch {
	case strings.HasSuffix(strings.ToLower(filename), ".tar.gz"):
		return extractTarGz(r)
	case strings.HasSuffix(strings.ToLower(filename), ".zip"):
		return extractZip(r)
	default:
		return nil, fmt.Errorf("unsupported archive format: %s", filepath.Ext(filename))
	}
}

func extractTarGz(r io.Reader) (map[string][]byte, error) {
	gzipReader, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("create gzip reader: %w", err)
	}
	defer gzipReader.Close()

	tarReader := tar.NewReader(gzipReader)
	files := make(map[string][]byte)
	var totalSize int64

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			return files, nil
		}
		if err != nil {
			return nil, fmt.Errorf("read tar entry: %w", err)
		}
		if header.FileInfo().IsDir() {
			continue
		}
		if shouldSkip(header.Name) {
			continue
		}
		if header.Size < 0 {
			return nil, fmt.Errorf("invalid tar entry size for %q", header.Name)
		}

		totalSize += header.Size
		if totalSize > maxTotalUncompressedSize {
			return nil, fmt.Errorf("archive exceeds 500MB uncompressed size limit")
		}

		content, err := io.ReadAll(tarReader)
		if err != nil {
			return nil, fmt.Errorf("read tar entry %q: %w", header.Name, err)
		}

		files[normalizePath(header.Name)] = content
	}
}

func extractZip(r io.Reader) (map[string][]byte, error) {
	archiveBytes, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read zip archive: %w", err)
	}

	zipReader, err := zip.NewReader(bytes.NewReader(archiveBytes), int64(len(archiveBytes)))
	if err != nil {
		return nil, fmt.Errorf("open zip archive: %w", err)
	}

	files := make(map[string][]byte)
	var totalSize uint64

	for _, file := range zipReader.File {
		if file.FileInfo().IsDir() {
			continue
		}
		if shouldSkip(file.Name) {
			continue
		}

		totalSize += file.UncompressedSize64
		if totalSize > maxTotalUncompressedSize {
			return nil, fmt.Errorf("archive exceeds 500MB uncompressed size limit")
		}

		reader, err := file.Open()
		if err != nil {
			return nil, fmt.Errorf("open zip entry %q: %w", file.Name, err)
		}

		content, readErr := io.ReadAll(reader)
		closeErr := reader.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read zip entry %q: %w", file.Name, readErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close zip entry %q: %w", file.Name, closeErr)
		}

		files[normalizePath(file.Name)] = content
	}

	return files, nil
}

func normalizePath(name string) string {
	for strings.HasPrefix(name, "./") {
		name = strings.TrimPrefix(name, "./")
	}
	for strings.HasPrefix(name, "/") {
		name = strings.TrimPrefix(name, "/")
	}

	return name
}

func shouldSkip(name string) bool {
	base := filepath.Base(name)
	return base == ".DS_Store" || strings.HasPrefix(base, "._")
}
