package teams

// Error describes a rejected team workflow.
type Error struct{ Code, Message string }

func (e *Error) Error() string { return e.Message }

func internalError(err error) error { return &Error{Code: "internal", Message: err.Error()} }
