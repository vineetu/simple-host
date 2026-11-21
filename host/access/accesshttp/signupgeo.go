package accesshttp

import (
	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/web"
)

type signupGeoBlock = accounts.SignupGeoBlock

// signupBlockedCode is the stable error code for a refused sign-in/sign-up.
const signupBlockedCode = "signup_unavailable_region"

// signupBlockedError is the JSON body for a refused sign-in/sign-up (403).
var signupBlockedError = httpx.ErrorResponse{Error: web.SignupBlockedMessage, Code: signupBlockedCode}
