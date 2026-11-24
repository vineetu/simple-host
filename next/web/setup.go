package web

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"

	cw "simplehost/core/web"
)

// setupCode is the one-time code that proves the person finishing setup
// runs this machine: made at the first start (or first visit) while there
// is no operator, printed only to the service's own log, kept only in memory.
// A database error leaves it unmade, to be made on the next try.
type setupCode struct {
	mu   sync.Mutex
	code string
}

func (h *handler) setupCode() string {
	s := &h.firstRun
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.code == "" {
		if done, err := h.a.People.HasOperator(context.Background()); err == nil && !done {
			b := make([]byte, 8)
			rand.Read(b)
			s.code = hex.EncodeToString(b)
			log.Printf("first-run setup code: %s (open https://%s/setup and enter it)", s.code, h.a.Config.AppDomain)
		}
	}
	return s.code
}

// setupPage is the first-run page of a self-hosted install: it shows the
// domain from .env, checks its DNS and makes the first account the operator.
// Once an operator exists, it is gone. The hosted service has none.
func (h *handler) setupPage(w http.ResponseWriter, r *http.Request) error {
	if !h.a.Config.SelfHosted {
		return cw.NotFound
	}
	if done, err := h.a.People.HasOperator(r.Context()); err != nil || done {
		return cmp(err, cw.NotFound)
	}
	h.setupCode()
	body, err := fs.ReadFile(static, "setup.html")
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write([]byte(strings.ReplaceAll(string(body), "__DOMAIN__", h.a.Config.AppDomain)))
	return nil
}

// setup checks that the domain and every name under it reach this server,
// then makes the given email the operator and signs them in. With
// {"check": true} it only reports the DNS.
func (h *handler) setup(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Email string `json:"email"`
		Code  string `json:"code"`
		Check bool   `json:"check"`
	}
	if !h.a.Config.SelfHosted {
		return cw.NotFound
	}
	if err := cw.Read(r, &in); err != nil {
		return err
	}
	if done, err := h.a.People.HasOperator(r.Context()); err != nil || done {
		return cmp(err, cw.NotFound)
	}
	domain := h.a.Config.AppDomain
	records := []dnsCheck{{Name: domain}, {Name: "setup-check--x." + h.a.Config.SiteDomain, Shows: "*." + h.a.Config.SiteDomain}}
	ok := true
	for i := range records {
		records[i].Addresses = h.resolve(r, records[i].Name)
		if records[i].Shows == "" {
			records[i].Shows = records[i].Name
		}
		records[i].OK = len(records[i].Addresses) > 0 && (i == 0 || slices.Equal(records[i].Addresses, records[0].Addresses))
		ok = ok && records[i].OK
	}
	if in.Check || !ok {
		cw.JSON(w, http.StatusOK, map[string]any{"ok": ok, "records": records})
		return nil
	}
	if code := h.setupCode(); code == "" || subtle.ConstantTimeCompare([]byte(strings.TrimSpace(in.Code)), []byte(code)) != 1 {
		return cw.Refuse(http.StatusForbidden, "bad_setup_code", "Enter the setup code the service printed in its log when it first started.")
	}
	person, made, err := h.a.People.FirstOperator(r.Context(), in.Email)
	if err != nil {
		return err
	}
	if !made {
		return cw.NotFound
	}
	if err := h.a.People.StartSession(w, r, person.ID); err != nil {
		return err
	}
	cw.JSON(w, http.StatusOK, map[string]any{"ok": true, "records": records, "next": "/dashboard"})
	return nil
}

type dnsCheck struct {
	Name      string   `json:"-"`
	Shows     string   `json:"name"`
	Addresses []string `json:"addresses"`
	OK        bool     `json:"ok"`
}

// resolve asks the DNS service (dns.google's JSON API) for a name's IPv4
// addresses, sorted. A failed lookup is no addresses.
func (h *handler) resolve(r *http.Request, name string) []string {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, h.a.Config.DNSURL+"?type=A&name="+url.QueryEscape(name), nil)
	if err != nil {
		return nil
	}
	res, err := h.a.HTTP.Do(req)
	if err != nil {
		return nil
	}
	defer res.Body.Close()
	var out struct {
		Answer []struct {
			Type int
			Data string
		}
	}
	json.NewDecoder(res.Body).Decode(&out)
	var ips []string
	for _, a := range out.Answer {
		if a.Type == 1 {
			ips = append(ips, a.Data)
		}
	}
	slices.Sort(ips)
	return ips
}

// cmp returns err when set, else other.
func cmp(err, other error) error {
	if err != nil {
		return err
	}
	return other
}
