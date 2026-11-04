package sites

import (
	"io"

	"github.com/vsriram/simple-host/internal/edition"
	"github.com/vsriram/simple-host/internal/storage"
	"github.com/vsriram/simple-host/internal/wpimport"
)

type ImportInput = wpimport.Input
type ImportResult = wpimport.Result
type ImportReport = wpimport.Report
type ImportFeature = wpimport.Feature
type ImportToolCall = wpimport.ToolCall
type ImportMediaFile = wpimport.MediaFile

const (
	ImportStatusNeedsSetup   = wpimport.StatusNeedsSetup
	ImportStatusBuiltIn      = wpimport.StatusBuiltIn
	ImportStatusReplaced     = wpimport.StatusReplaced
	ImportStatusNotSupported = wpimport.StatusNotSupported
)

// ReceiveUpload stages and unpacks one browser upload under the import's budgets.
func (f *ImportFiles) ReceiveUpload(src io.ReadCloser) error {
	dst, err := storage.CreateImportFile(f.Dir, "upload-")
	if err != nil {
		src.Close()
		return err
	}
	_, err = io.Copy(dst, src)
	closeErr := dst.Close()
	src.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return f.Unpack(dst.Name())
}

// BeginImportUpload creates the workspace and its budgeted entry collector together.
func BeginImportUpload(limits edition.Limits) (*ImportFiles, error) {
	dir, err := storage.CreateImportDir()
	if err != nil {
		return nil, err
	}
	return &ImportFiles{Dir: dir, Limits: limits, Media: map[string]ImportMediaFile{}}, nil
}

// DiscardUpload releases an upload which was not handed to the import worker.
func (f *ImportFiles) DiscardUpload() { storage.RemoveImportDir(f.Dir) }
