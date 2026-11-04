package storage

// Error identifies a storage workflow failure.
type Error struct {
	Code, Message string
	Rule          string
	Allowed       bool
	NoRetryAfter  bool
	Refusals      []*Error
}

func (e *Error) Error() string { return e.Message }
