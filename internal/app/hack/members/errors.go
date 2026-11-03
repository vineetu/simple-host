package members

// Error describes a membership workflow refusal.
type Error struct{ Code, Message string }

func (e *Error) Error() string { return e.Message }
