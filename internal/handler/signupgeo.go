package handler

import "github.com/vsriram/simple-host/internal/app/accounts"

type signupGeoBlock = accounts.SignupGeoBlock

// signupBlockedCode is the stable error code for a refused sign-in/sign-up.
const signupBlockedCode = "signup_unavailable_region"

// signupBlockedMessage names no country and no reason beyond "your region" —
// shown verbatim to the person, never with sanctions or policy language. It
// covers both a refused new signup and a refused existing sign-in.
const signupBlockedMessage = "Sign-in isn't available from your region."

// signupBlockedError is the JSON body for a refused sign-in/sign-up (403).
var signupBlockedError = errorResponse{Error: signupBlockedMessage, Code: signupBlockedCode}
