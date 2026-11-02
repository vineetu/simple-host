package imagescan

import (
	"context"
	"fmt"
	"sync"
)

// Fake is a Matcher and Reporter for tests. Matches is keyed by SHA256 of
// the original bytes.
type Fake struct {
	mu        sync.Mutex
	Matches   map[string]Match // sha256 -> what to answer (IsMatch etc.); absent -> clean
	Err       error            // returned by Match when set
	ReportErr error            // returned by Report when set
	Calls     []string         // sha256 of each Match call, in order
	Reports   []Report         // every Report call
}

// Name implements Matcher.
func (f *Fake) Name() string { return "fake" }

// Match records the call and answers Matches[img.SHA256], or a clean Match
// with TrackingID "fake-<n>" when the hash is not listed.
func (f *Fake) Match(ctx context.Context, img *Image) (Match, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	sha := ""
	if img != nil {
		sha = img.SHA256
	}
	f.Calls = append(f.Calls, sha)
	if f.Err != nil {
		return Match{}, f.Err
	}
	if m, ok := f.Matches[sha]; ok {
		return m, nil
	}
	return Match{TrackingID: fmt.Sprintf("fake-%d", len(f.Calls))}, nil
}

// Report records the call and answers ReportResult{ReportID: "fake-report-<n>"}.
func (f *Fake) Report(ctx context.Context, in Report) (ReportResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Reports = append(f.Reports, in)
	if f.ReportErr != nil {
		return ReportResult{}, f.ReportErr
	}
	return ReportResult{ReportID: fmt.Sprintf("fake-report-%d", len(f.Reports))}, nil
}
