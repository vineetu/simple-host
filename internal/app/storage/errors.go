package storage

// Error identifies a storage workflow failure.
type Error struct{ Code, Message string }

func (e *Error) Error() string { return e.Message }
