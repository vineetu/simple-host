package storage

// Error identifies a storage workflow failure.
type Error struct {
	Code, Message string
	Allowed       bool
	Refusals      []*Error
}

func (e *Error) Error() string { return e.Message }
