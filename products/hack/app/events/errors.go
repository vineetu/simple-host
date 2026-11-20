package events

import hackmodel "github.com/vsriram/simple-host/products/hack/model"

// Error is the shared hack workflow refusal.
type Error = hackmodel.Error

// RateLimitError is a DB-counted rate limit refusal (team messages):
// RetryAfter is the whole seconds to wait before trying again.
type RateLimitError struct{ RetryAfter int }

func (e *RateLimitError) Error() string { return "rate limit exceeded, slow down" }
