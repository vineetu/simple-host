package sites

// Error describes a site workflow failure without prescribing an HTTP response.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Message }

// GateError describes a transaction-time team authorization refusal.
type GateError struct {
	Message string
	Code    string
}

func (e *GateError) Error() string { return e.Message }
