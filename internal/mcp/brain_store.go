package mcp

import (
	"errors"

	"github.com/vsriram/simple-host/internal/brain"
)

// BrainStore adapts product knowledge to the HTTP consumer's narrow interface.
type BrainStore struct{ store *brain.Store }

func OpenBrainStore(path string) *BrainStore { return &BrainStore{brain.Open(path)} }
func (s *BrainStore) Call(name string, args map[string]any) (string, map[string]any, error) {
	result, err := s.store.Call(name, args)
	return result.Text, result.Structured, err
}
func (s *BrainStore) Freshness() (map[string]any, error) { return s.store.Freshness() }
func (s *BrainStore) ErrorCode(err error) string {
	var arg brain.ArgError
	if errors.As(err, &arg) {
		return "invalid_argument"
	}
	if errors.Is(err, brain.ErrNotReady) {
		return "not_ready"
	}
	return ""
}
