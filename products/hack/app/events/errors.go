package events

// Error describes a workflow failure for the transport to map.
type Error struct{ Code, Message string }

func (e *Error) Error() string { return e.Message }

// RateLimitError is a DB-counted rate limit refusal (team messages):
// RetryAfter is the whole seconds to wait before trying again.
type RateLimitError struct{ RetryAfter int }

func (e *RateLimitError) Error() string { return "rate limit exceeded, slow down" }
