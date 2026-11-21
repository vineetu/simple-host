package sites

import (
	"context"
	"net/http"
	"os"
)

// Serving is one request for a site's file on its way through the visitor
// gate (take-down, offline, passcode, named viewers). The site handler opens
// the site's directory, puts the Serving on the request and hands it to the
// gate, which answers a refusal itself or passes the request to Then.
//
// A Serving with no site (UserID "") is a protected request on a host the
// app does not know: the gate answers the closed page.
type Serving struct {
	UserID, Name string
	// Rel is the path inside the site ("/" is its root).
	Rel string
	// Root is the site's directory as opened for this request; its marker
	// files are the ones the gate reads.
	Root *os.Root
	// FrameOnly: the request carries its own admission (a preview link), so
	// the gate applies only the owner's frame guard.
	FrameOnly bool
	// Then serves the file once the gate lets the request in.
	Then http.Handler
}

type servingKey struct{}

// WithServing puts s on r for the gate.
func WithServing(r *http.Request, s Serving) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), servingKey{}, s))
}

// ServingOf is the Serving the site handler put on r.
func ServingOf(r *http.Request) (Serving, bool) {
	s, ok := r.Context().Value(servingKey{}).(Serving)
	return s, ok
}
