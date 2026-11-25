// Package compat is the throwaway translation layer for callers of the
// service before it was rebuilt: old HTTP addresses (routes.go), old tool
// names (tools.go), the stale-skill notice, and a count of who still uses
// them. It answers until the sunset date, then steps aside and every old
// way gets the service's own "changed" refusal. Delete it: see README.md.
package compat

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"time"

	"simplehost/app"
	"simplehost/core/web"
)

// until is the sunset: 30 days after the build (2026-11-25). SH_COMPAT_UNTIL
// (YYYY-MM-DD) moves it.
const until = "2026-12-25"

// Until is the first moment past the sunset day.
func Until() time.Time {
	day, err := time.Parse(time.DateOnly, strings.TrimSpace(os.Getenv("SH_COMPAT_UNTIL")))
	if err != nil {
		day, _ = time.Parse(time.DateOnly, until)
	}
	return day.Add(24 * time.Hour)
}

func over() bool { return !time.Now().Before(Until()) }

const skillNotice = "Your Simple Host skill is out of date. Reinstall it from https://simple-host.app/skills.zip, then restart your agent or start a new chat."

type compat struct {
	a    *app.App
	next http.Handler
	// old: addresses only old callers use. shaped: addresses still in use,
	// answered the old way when the caller is old (an old query or skill).
	old, shaped *http.ServeMux
}

// Wrap puts compat in front of the service (next) on the platform host, and
// adds its one operator route. main serves what it returns.
func Wrap(a *app.App, next http.Handler) http.Handler {
	if _, err := a.DB.Exec(table); err != nil {
		log.Printf("compat: %v", err)
	}
	c := &compat{a: a, next: next, old: http.NewServeMux(), shaped: http.NewServeMux()}
	c.routes()
	a.Routes.Platform.Handle("GET /v1/admin/compat", web.Func(c.report))
	return c
}

func (c *compat) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if web.Host(r) != c.a.Config.AppDomain || over() {
		c.next.ServeHTTP(w, r)
		return
	}
	skill := r.Header.Get("X-Skill-Version") != ""
	var mux http.Handler = c.old
	_, name := c.old.Handler(r)
	if name == "" && (skill || oldQuery(r)) {
		mux = c.shaped
		_, name = c.shaped.Handler(r)
	}
	if name == "" && skill {
		_, p := c.a.Routes.Platform.Handler(r)
		mux, name = c.next, "skill: "+p
	}
	if name == "" {
		c.next.ServeHTTP(w, r)
		return
	}
	who, err := c.a.People.Caller(r, "")
	if err != nil {
		who = web.Caller{} // the service refuses the credential itself
	}
	r = web.WithCaller(r, who)
	count(c.a, r, name)
	h := w.Header()
	h.Set("Deprecation", "true")
	h.Set("Sunset", Until().UTC().Format(http.TimeFormat))
	h.Set("Link", `</llms.txt>; rel="successor-version"`)
	if !skill {
		mux.ServeHTTP(w, r)
		return
	}
	// The old skill's signal: X-Skill-Notice, and _notice in a JSON object.
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, r)
	for k, v := range rec.Header() {
		h[k] = v
	}
	body := rec.Body.Bytes()
	if strings.HasPrefix(h.Get("Content-Type"), "application/json") {
		h.Set("X-Skill-Notice", skillNotice)
		var obj map[string]json.RawMessage
		if json.Unmarshal(body, &obj) == nil && obj != nil {
			obj["_notice"], _ = json.Marshal(skillNotice)
			body, _ = json.Marshal(obj)
			h.Del("Content-Length")
		}
	}
	w.WriteHeader(rec.Code)
	w.Write(body)
}

// oldQuery: a query only the old service took.
func oldQuery(r *http.Request) bool {
	q := r.URL.Query()
	for _, k := range []string{"create", "keep", "publish", "access", "viewers", "author", "all", "files", "order", "query", "limit", "offset"} {
		if q.Has(k) {
			return true
		}
	}
	return false
}

// answer is one call through the service.
type answer struct {
	status int
	header http.Header
	body   []byte
}

// send runs one request through the service as the caller, with the
// caller's own credentials and address. body is []byte (sent as it is, with
// the caller's Content-Type) or anything else (sent as JSON).
func (c *compat) send(r *http.Request, method, path string, body any) answer {
	var rd []byte
	req := r.Clone(r.Context())
	switch b := body.(type) {
	case nil:
		req.Header.Del("Content-Type")
	case []byte:
		rd = b
	default:
		rd, _ = json.Marshal(b)
		req.Header.Set("Content-Type", "application/json")
	}
	u, _ := url.Parse(path)
	req.Method, req.URL, req.RequestURI = method, u, u.RequestURI()
	req.Body, req.ContentLength = io.NopCloser(bytes.NewReader(rd)), int64(len(rd))
	rec := httptest.NewRecorder()
	c.next.ServeHTTP(rec, req)
	return answer{rec.Code, rec.Header(), rec.Body.Bytes()}
}

// do is send for a JSON answer: the object, or the refusal as the error.
func (c *compat) do(r *http.Request, method, path string, body any) (map[string]any, error) {
	a := c.send(r, method, path, body)
	var v map[string]any
	json.Unmarshal(a.body, &v)
	if a.status >= 400 {
		ref := web.Refuse(a.status, "not_found", "Nothing is there. Check the site and the path.")
		if code, _ := v["code"].(string); code != "" {
			ref.Code, _ = v["code"].(string)
			ref.Fix, _ = v["fix"].(string)
			ref.Message, _ = v["message"].(string)
		}
		return nil, ref
	}
	if v == nil {
		v = map[string]any{}
	}
	return v, nil
}

// pass writes an answer as it came.
func pass(w http.ResponseWriter, a answer) {
	for _, k := range []string{"Content-Type", "Cache-Control", "Content-Disposition"} {
		if v := a.header.Get(k); v != "" {
			w.Header().Set(k, v)
		}
	}
	w.WriteHeader(a.status)
	w.Write(a.body)
}

// The count: per day, per person ("anonymous" when nobody), per old route
// or tool name. Owned here and dropped with the package.
const table = `CREATE TABLE IF NOT EXISTS compat_calls (
  day   DATE NOT NULL,
  who   TEXT NOT NULL,
  name  TEXT NOT NULL,
  calls BIGINT NOT NULL,
  PRIMARY KEY (day, who, name))`

func count(a *app.App, r *http.Request, name string) {
	who := web.CallerOf(r).ID
	if who == "" {
		who = "anonymous"
	}
	_, err := a.DB.ExecContext(r.Context(), `INSERT INTO compat_calls VALUES ((now() AT TIME ZONE 'UTC')::date, $1, $2, 1)
		ON CONFLICT (day, who, name) DO UPDATE SET calls = compat_calls.calls + 1`, who, name)
	if err != nil {
		log.Printf("compat count: %v", err)
	}
}

// report is GET /v1/admin/compat: who still uses which old way, most calls
// first, so they can be told before the sunset.
func (c *compat) report(w http.ResponseWriter, r *http.Request) error {
	if !web.CallerOf(r).Operator {
		return web.NotFound
	}
	rows, err := c.a.DB.QueryContext(r.Context(), `SELECT c.who, COALESCE(p.email, ''), COALESCE(n.name, ''), c.name, sum(c.calls), max(c.day)
		FROM compat_calls c LEFT JOIN people p ON p.id::text = c.who
		LEFT JOIN names n ON n.person_id = p.id AND n.kind = 'handle' AND n.retired_at IS NULL
		GROUP BY 1, 2, 3, 4 ORDER BY sum(sum(c.calls)) OVER (PARTITION BY c.who) DESC, c.who, 5 DESC`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type use struct {
		Name    string `json:"name"`
		Calls   int64  `json:"calls"`
		LastDay string `json:"last_day"`
	}
	type person struct {
		Who    string `json:"who"`
		Email  string `json:"email,omitempty"`
		Handle string `json:"handle,omitempty"`
		Calls  int64  `json:"calls"`
		Uses   []use  `json:"uses"`
	}
	people := []*person{}
	for rows.Next() {
		var p person
		var u use
		var day time.Time
		if err := rows.Scan(&p.Who, &p.Email, &p.Handle, &u.Name, &u.Calls, &day); err != nil {
			return err
		}
		u.LastDay = day.Format(time.DateOnly)
		if n := len(people); n == 0 || people[n-1].Who != p.Who {
			people = append(people, &p)
		}
		last := people[len(people)-1]
		last.Calls += u.Calls
		last.Uses = append(last.Uses, u)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	web.JSON(w, http.StatusOK, map[string]any{"until": Until().Add(-24 * time.Hour).Format(time.DateOnly), "people": people})
	return nil
}
