package events

// Error describes a workflow failure for the transport to map.
type Error struct{ Code, Message string }

func (e *Error) Error() string { return e.Message }
