package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
)

// NoticeMiddleware returns a middleware that tells a caller whose
// `X-Skill-Version` header is missing or stale to update its skill: every
// JSON response carries the notice in an `X-Skill-Notice` header, and a JSON
// object also gains a top-level `_notice` field. A response never changes
// shape because of the header: a top-level array stays a bare array (the
// header is its notice), and an object keeps every field exactly as written.
//
// Scoping is structural — only routes whose Register methods accept this
// middleware as a parameter receive it. State endpoints, static serving,
// skill downloads, and health probes are deliberately *not* wrapped, so
// browser pages and binary responses pass through untouched.
//
// Defense in depth: even if a non-JSON route ever ends up wrapped, the
// Content-Type guard inside no-ops on responses that aren't
// application/json, so HTML/binary/raw bytes pass through unchanged.
func NoticeMiddleware(serverVersion string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			clientVer := r.Header.Get("X-Skill-Version")
			stale := skillIsStale(clientVer, serverVersion)

			// Version files are raw owner content, including JSON. Never buffer
			// or inject notices into this route's response.
			if !stale || r.Pattern == "GET /v1/sites/{sitename}/versions/{version}/files/{path...}" {
				next.ServeHTTP(w, r)
				return
			}

			rec := &bufferingResponseWriter{header: make(http.Header)}
			next.ServeHTTP(rec, r)

			for k, vs := range rec.header {
				w.Header()[k] = vs
			}

			contentType := rec.header.Get("Content-Type")
			isJSON := strings.HasPrefix(contentType, "application/json")
			if !isJSON {
				if rec.status != 0 {
					w.WriteHeader(rec.status)
				}
				_, _ = w.Write(rec.body.Bytes())
				return
			}

			notice := noticeText(serverVersion, requestBaseURL(r))
			w.Header().Set("X-Skill-Notice", notice)
			injected, ok := injectNotice(rec.body.Bytes(), notice)
			if !ok {
				if rec.status != 0 {
					w.WriteHeader(rec.status)
				}
				_, _ = w.Write(rec.body.Bytes())
				return
			}

			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(injected)))
			if rec.status != 0 {
				w.WriteHeader(rec.status)
			}
			if _, err := w.Write(injected); err != nil {
				log.Printf("notice middleware write: %v", err)
			}
		})
	}
}

// skillIsStale reports whether the caller's skill should be told to update.
//
// A missing header means "no skill version claimed" — always notify. Otherwise
// notify only when the SERVER is genuinely newer. A client that is ahead of the
// server is not stale: `npx skills add` installs straight from the source repository
// repository, so a user can legitimately be running a version that this server
// has not been redeployed with yet. Telling them to "update" would send them in
// a circle. An unparseable version falls back to plain inequality.
func skillIsStale(clientVer, serverVersion string) bool {
	if clientVer == "" {
		return true
	}
	if clientVer == serverVersion {
		return false
	}
	client, okC := parseSemver(clientVer)
	server, okS := parseSemver(serverVersion)
	if !okC || !okS {
		return true // can't compare — preserve the old any-difference behavior
	}
	for i := 0; i < 3; i++ {
		if client[i] != server[i] {
			return client[i] < server[i]
		}
	}
	return false
}

// parseSemver reads a plain MAJOR.MINOR.PATCH string. Pre-release and build
// suffixes are not used by this plugin, so anything else is "unparseable".
func parseSemver(v string) ([3]int, bool) {
	var out [3]int
	parts := strings.Split(strings.TrimSpace(v), ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

type bufferingResponseWriter struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (b *bufferingResponseWriter) Header() http.Header {
	return b.header
}

func (b *bufferingResponseWriter) WriteHeader(status int) {
	if b.status == 0 {
		b.status = status
	}
}

func (b *bufferingResponseWriter) Write(p []byte) (int, error) {
	if b.status == 0 {
		b.status = http.StatusOK
	}
	return b.body.Write(p)
}

// requestBaseURL reconstructs the scheme://host the caller actually reached us
// on, so install/help URLs in responses point at THIS instance — not a
// hardcoded domain. Behind nginx, Host is preserved and X-Forwarded-Proto
// carries the original scheme (see the proxy config); we fall back to the
// request's own TLS state, then https.
func requestBaseURL(r *http.Request) string {
	scheme := r.Header.Get("X-Forwarded-Proto")
	if scheme == "" {
		// This is a public HTTPS service; default to https when the proxy
		// header is absent (e.g. direct local calls) rather than guessing http.
		scheme = "https"
	}
	host := r.Host
	if host == "" {
		host = "simple-host.app"
	}
	return scheme + "://" + host
}

// injectNotice adds a top-level `_notice` field to a JSON object, splicing
// it in after the opening brace so every other field keeps its exact bytes
// (numbers are never re-encoded). Anything else (an array, a scalar, invalid
// JSON) comes back ok=false and is passed through unchanged.
func injectNotice(raw []byte, notice string) ([]byte, bool) {
	body := bytes.TrimSpace(raw)
	if len(body) < 2 || body[0] != '{' || !json.Valid(body) {
		return nil, false
	}
	field, err := json.Marshal(notice)
	if err != nil {
		return nil, false
	}
	rest := bytes.TrimSpace(body[1:])
	out := make([]byte, 0, len(body)+len(field)+13)
	out = append(out, `{"_notice":`...)
	out = append(out, field...)
	if rest[0] != '}' {
		out = append(out, ',')
	}
	return append(out, rest...), true
}

func noticeText(version, baseURL string) string {
	return "Your website-deploy skill is out of date. Latest is " + version +
		". Update: npx skills add vineetu/simple-host (other ways: " + baseURL + "/docs.html#install-skills). Then restart your agent (Claude Code) or re-invoke the skill (Codex CLI / Cursor)."
}
