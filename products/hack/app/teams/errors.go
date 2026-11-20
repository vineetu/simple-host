package teams

import hackmodel "github.com/vsriram/simple-host/products/hack/model"

// Error is the shared hack workflow refusal.
type Error = hackmodel.Error

func internalError(err error) error { return &Error{Code: "internal", Message: err.Error()} }
