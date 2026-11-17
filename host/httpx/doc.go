// Package httpx holds the HTTP protocol mechanics shared by every transport
// in this binary: JSON and ETag response writing, the error body shape,
// token-bucket rate limiting and client-IP resolution, CSP nonce stamping,
// and small request-header helpers (Accept negotiation, CSV cell safety). It
// carries no application orchestration, no SQL and no transport-specific
// routing, and (per docs/architecture/target.md §2) imports no other
// internal package.
package httpx
