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

// NoticeMiddleware returns a middleware that injects a `_notice` field
// into JSON responses when the caller's `X-Skill-Version` header is
// missing or stale. The notice text directs the caller to re-run the
// install script.
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

			if !stale {
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

			injected, ok := injectNotice(rec.body.Bytes(), serverVersion, requestBaseURL(r))
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
// server is not stale: `npx skills add` installs straight from the GitHub
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

// injectNotice decodes JSON, adds a top-level `_notice` field, and
// re-encodes. For top-level arrays, the result is wrapped as
// `{data: [...], _notice: ...}`. Returns ok=false on decode failure so
// the caller can pass the original body through unchanged.
func injectNotice(raw []byte, version, baseURL string) ([]byte, bool) {
	notice := noticeText(version, baseURL)

	var asObject map[string]any
	if err := json.Unmarshal(raw, &asObject); err == nil && asObject != nil {
		asObject["_notice"] = notice
		out, err := json.Marshal(asObject)
		if err != nil {
			return nil, false
		}
		return out, true
	}

	var asArray []any
	if err := json.Unmarshal(raw, &asArray); err == nil {
		wrapped := map[string]any{"data": asArray, "_notice": notice}
		out, err := json.Marshal(wrapped)
		if err != nil {
			return nil, false
		}
		return out, true
	}

	return nil, false
}

func noticeText(version, baseURL string) string {
	return "Your website-deploy skill is out of date. Latest is " + version +
		". Update — macOS/Linux: curl -fsSL " + baseURL + "/install.sh | sh ; Windows PowerShell: irm " + baseURL + "/install.ps1 | iex . Then restart your agent (Claude Code) or re-invoke the skill (Codex CLI / Cursor)."
}
