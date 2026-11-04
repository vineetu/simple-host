package sites

import "github.com/vsriram/simple-host/internal/wpimport"

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
