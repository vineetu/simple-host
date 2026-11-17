package httpx

import (
	"bytes"
	"net/http"
)

// NonceKey is the context key an inline CSP nonce is stored under for the
// request. Its identity must stay exactly this type (a zero-field struct),
// not a renamed or redefined one, so the CSP middleware that sets it and
// StampNonce that reads it keep agreeing on the same key.
type NonceKey struct{}

// StampNonce gives the page's bare scripts and JSON-LD blocks the request's CSP
// nonce. Data spliced into a page (json.Marshal escapes '<') cannot contain
// either literal tag. Without a nonce in the request's context, page is
// returned as is.
func StampNonce(r *http.Request, page []byte) []byte {
	nonce, _ := r.Context().Value(NonceKey{}).(string)
	if nonce == "" {
		return page
	}
	page = bytes.ReplaceAll(page, []byte("<script>"), []byte(`<script nonce="`+nonce+`">`))
	return bytes.ReplaceAll(page, []byte(`<script type="application/ld+json">`), []byte(`<script type="application/ld+json" nonce="`+nonce+`">`))
}
