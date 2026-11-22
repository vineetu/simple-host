package versions

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf8"

	"github.com/vsriram/simple-host/host/sites"
)

// MaxFileText bounds how much of one file a text read hands back, so one
// answer cannot flood a conversation.
const MaxFileText = 200 << 10

// FileText is one file of a site version read as text
// (GET /v1/sites/{sitename}/versions/{version}/files?read=<path>).
type FileText struct {
	Path        string `json:"path"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type,omitempty"`
	// Content is the file's text, at most MaxFileText bytes; absent for a
	// binary file (image, font, audio, video, or bytes that are not text).
	Content   *string `json:"content,omitempty"`
	Binary    bool    `json:"binary,omitempty"`
	Truncated bool    `json:"truncated,omitempty"`
	// Error and Status say why this one file could not be read, as the
	// single-file route would answer it (400 invalid path, 404 not found).
	Error  string `json:"error,omitempty"`
	Status int    `json:"status,omitempty"`
}

// ReadVersionTexts reads each path of one version as text; a file that cannot
// be read carries its own error and does not hide the others.
func ReadVersionTexts(ctx context.Context, files sites.SiteFiles, ref sites.FileVersion, paths []string) ([]FileText, error) {
	out := make([]FileText, 0, len(paths))
	for _, path := range paths {
		f, err := readVersionText(ctx, files, ref, path)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, nil
}

func readVersionText(ctx context.Context, files sites.SiteFiles, ref sites.FileVersion, path string) (FileText, error) {
	f := FileText{Path: path}
	if !sites.ValidVersionFilePath(path) {
		f.Error, f.Status = "invalid path", http.StatusBadRequest
		return f, nil
	}
	file, info, err := files.OpenFile(ctx, ref, path)
	switch {
	case errors.Is(err, sites.ErrInvalidFilePath):
		f.Error, f.Status = "invalid path", http.StatusBadRequest
		return f, nil
	case os.IsNotExist(err), errors.Is(err, syscall.ENOTDIR):
		f.Error, f.Status = "file not found", http.StatusNotFound
		return f, nil
	case err != nil:
		return f, err
	}
	defer file.Close()
	if info.IsDir() {
		f.Error, f.Status = "file not found", http.StatusNotFound
		return f, nil
	}
	body, err := io.ReadAll(file)
	if err != nil {
		return f, err
	}
	f.Size, f.ContentType = int64(len(body)), VersionFileType(path)
	ctype := f.ContentType
	textual := utf8.Valid(body) && !bytes.ContainsRune(body, 0) &&
		!strings.HasPrefix(ctype, "image/") && !strings.HasPrefix(ctype, "font/") && !strings.HasPrefix(ctype, "audio/") && !strings.HasPrefix(ctype, "video/")
	if !textual {
		f.Binary = true
		return f, nil
	}
	text := string(body)
	if len(text) > MaxFileText {
		text = text[:MaxFileText]
		for !utf8.ValidString(text) {
			text = text[:len(text)-1]
		}
		f.Truncated = true
	}
	f.Content = &text
	return f, nil
}

// VersionFileType is the Content-Type a version file is served with.
func VersionFileType(path string) string {
	contentType := mime.TypeByExtension(filepath.Ext(path))
	if contentType == "" {
		return "application/octet-stream"
	}
	if contentType == "text/html" {
		contentType += "; charset=utf-8"
	}
	return contentType
}
