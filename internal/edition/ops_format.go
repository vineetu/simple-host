package edition

import (
	"github.com/vsriram/simple-host/internal/config"
	"time"
)

// Span formats an operational duration in the same words as the configured limits.
func (e *Edition) Span(d time.Duration) string { return config.Span(d) }
