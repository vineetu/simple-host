package handler

import (
	"database/sql"
	"net/http"
	"sync"

	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/httpx"
)

// AccessHandler serves who may use a site: accounts, passcodes, named
// viewers, visitor sign-in and sessions, suspension. site resolves sites and
// addresses for it, data serves the visitor tools' saved data.
type AccessHandler struct {
	site *SiteHandler
	data *DataHandler
	ed   *edition.Edition
	limitSet
	database           *sql.DB
	disk               SiteDisk
	mailer             signInMailer
	emailLimiter       *httpx.RateLimiter
	visitorAuthLimiter *httpx.RateLimiter
	siteDomain         string
	contentHost        string
	exportKey          []byte
	uploadLocks        *sync.Map
	// adminAPIKey / adminUserID recognize owner/admin X-API-Key callers on
	// routes that are not wrapped with auth.Middleware (the site storage
	// routes).
	adminAPIKey string
	adminUserID string

	// passcode is the site-passcode key and wrong-try counters (passcode.go).
	passcode   passcodeState
	ssoStart   http.Handler
	ssoBaseURL string
	// noVisitorSignIn: this install has no way for a visitor to sign in (no
	// email codes, no Google/GitHub). noEmail: it sends no email. Both false
	// unless SetVisitorSignIn says. viewerGoogle: Google sign-in is set up,
	// so the sign-in page offers it next to the emailed code.
	noVisitorSignIn, noEmail, viewerGoogle bool
	// geoBlock gates a visitor's email-code sign-in on a site by the
	// request's country (SIGNUP_BLOCKED_COUNTRIES). Zero value: off. See
	// signupgeo.go.
	geoBlock signupGeoBlock
}
