// Package signin is signing up, in and out (F1): an emailed code, or Google
// (or a company's own sign-in) proving the email. The email is the person.
// It also carries a visitor's sign-in for a site (F18) through the platform:
// whichever way the email is proved, a hop from the site comes back as a
// ticket for that site.
package signin

import (
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"

	access "simplehost/access/core"
	"simplehost/app"
	"simplehost/core/limits"
	"simplehost/core/web"
)

const (
	codeLife   = 15 * time.Minute
	codeTries  = 5
	ticketLife = 2 * time.Minute
	stateLife  = 10 * time.Minute
)

var (
	errBadCode = web.Refuse(http.StatusUnauthorized, "bad_code", "This code is wrong or has run out. Ask for a new one.")
	errSignIn  = web.Refuse(http.StatusUnauthorized, "sign_in", "Sign in first.")
	errState   = web.Refuse(http.StatusBadRequest, "sign_in_expired", "This sign-in was started in another browser or ran out. Start again.")
	errHop     = web.Refuse(http.StatusBadRequest, "bad_hop", "This sign-in link has run out. Go back to the site and sign in again.")
)

type handler struct{ a *app.App }

// Mount adds routes 1 to 5.
func Mount(a *app.App) {
	h := &handler{a}
	a.Routes.Platform.Handle("POST /v1/auth/code", web.Func(h.sendCode))
	a.Routes.Platform.Handle("POST /v1/auth/verify", web.Func(h.verify))
	a.Routes.Platform.Handle("GET /v1/auth/oauth/{provider}", web.Func(h.start))
	a.Routes.Platform.Handle("GET /v1/auth/oauth/{provider}/callback", web.Func(h.finish))
	a.Routes.Platform.Handle("POST /v1/auth/signout", web.Func(h.signOut))
	a.Routes.Platform.Handle("POST /v1/auth", web.Func(h.agentCode))
	a.Routes.Platform.Handle("POST /v1/auth/reviewer", web.Func(h.reviewer)) // review.go
}

// agentCode answers the old service's agent sign-in (POST /v1/auth, then a
// key from /v1/auth/verify). It is retired: an agent connects through MCP,
// or the person makes a key in the dashboard and gives it to the agent.
func (h *handler) agentCode(w http.ResponseWriter, r *http.Request) error {
	d := "https://" + h.a.Config.AppDomain
	return web.Refuse(http.StatusGone, "agent_sign_in_retired", "Sign-in by code from an agent is retired. Connect Simple Host through MCP ("+d+
		"/mcp), or create an API key at "+d+"/dashboard/account#keys and give it to your agent.")
}

func normalEmail(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if !strings.Contains(s, "@") || strings.ContainsAny(s, " \r\n") {
		return "", web.Refuse(http.StatusBadRequest, "bad_email", "Give a full email address, like name@example.com.")
	}
	return s, nil
}

// sendCode is route 1: email a six-digit sign-in code.
func (h *handler) sendCode(w http.ResponseWriter, r *http.Request) error {
	var in struct{ Email string }
	if err := web.Read(r, &in); err != nil {
		return err
	}
	email, err := normalEmail(in.Email)
	if err != nil {
		return err
	}
	if err := limits.Allow(r, limits.SignInCodes, email); err != nil {
		return err
	}
	if err := limits.Allow(r, limits.CodeMails, "mail:"+limits.Address(r)); err != nil {
		return err
	}
	n, _ := rand.Int(rand.Reader, big.NewInt(1_000_000))
	code := fmt.Sprintf("%06d", n.Int64())
	if _, err := h.a.DB.ExecContext(r.Context(), `INSERT INTO sign_in_codes (email, code_hash, expires_at) VALUES ($1, $2, $3)`,
		email, access.Hash(code), time.Now().Add(codeLife)); err != nil {
		return err
	}
	text := "Your Simple Host sign-in code is " + code + ". It works for 15 minutes.\n\nIf you did not ask for it, you can ignore this email."
	if err := h.a.Mail.Send(r.Context(), email, "Your Simple Host code: "+code, text); err != nil {
		return err
	}
	web.JSON(w, http.StatusOK, map[string]bool{"sent": true})
	return nil
}

// verify is route 2. With a code it proves the email, signs this browser
// in (making the person if new) and may claim a handle. With only a hop and
// a signed-in browser, it is "continue to this site as me": the first time
// per site the answer asks for confirm, sent back as confirm: true. With a
// hop, the answer's next is where to send the browser to finish on the site.
func (h *handler) verify(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	var in struct {
		Email, Code, Handle, Hop string
		Confirm                  bool
	}
	if err := web.Read(r, &in); err != nil {
		return err
	}
	var who access.Person
	var codeID int64
	fresh := in.Code != ""
	if fresh {
		email, err := normalEmail(in.Email)
		if err != nil {
			return err
		}
		// Every try is counted, in the same statement that checks the
		// count, before the code is compared: parallel guesses cannot pass
		// the limit.
		var hash string
		err = h.a.DB.QueryRowContext(ctx, `UPDATE sign_in_codes SET tries = tries + 1 WHERE id = (SELECT id FROM sign_in_codes
			WHERE email = $1 AND used_at IS NULL AND expires_at > now() ORDER BY id DESC LIMIT 1) AND tries < $2 RETURNING id, code_hash`,
			email, codeTries).Scan(&codeID, &hash)
		if err == sql.ErrNoRows {
			return errBadCode
		}
		if err != nil {
			return err
		}
		if subtle.ConstantTimeCompare([]byte(access.Hash(strings.TrimSpace(in.Code))), []byte(hash)) != 1 {
			return errBadCode
		}
		if who, err = h.a.People.Ensure(ctx, email); err != nil {
			return err
		}
	} else {
		c := web.CallerOf(r)
		if in.Hop == "" || c.Via != "session" {
			return web.Refuse(http.StatusBadRequest, "no_code", "Send your email and the code we emailed you.")
		}
		var err error
		if who, err = h.a.People.Get(ctx, c.ID); err != nil {
			return err
		}
	}
	if in.Handle != "" && who.Handle == "" {
		if err := access.ClaimName(ctx, h.a.DB, strings.ToLower(strings.TrimSpace(in.Handle)), "handle", who.ID, ""); err != nil {
			return err
		}
		who.Handle = strings.ToLower(strings.TrimSpace(in.Handle))
		h.a.Sites.Forget("") // a first handle: nothing was kept for it
	}
	if fresh {
		res, err := h.a.DB.ExecContext(ctx, `UPDATE sign_in_codes SET used_at = now() WHERE id = $1 AND used_at IS NULL`, codeID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return errBadCode
		}
		if err := h.a.People.StartSession(w, r, who.ID); err != nil {
			return err
		}
	}
	out := map[string]string{"id": who.ID, "email": who.Email, "handle": who.Handle}
	if in.Hop != "" {
		hop, err := access.OpenHop(in.Hop)
		if err != nil {
			return errHop
		}
		ok := fresh || in.Confirm
		if !ok {
			if ok, err = h.a.People.SiteConfirmed(ctx, hop.Site, who.ID); err != nil {
				return err
			}
		}
		if !ok {
			out["confirm"] = hop.Host
		} else {
			out["next"] = ticket(hop, who.ID)
		}
	}
	web.JSON(w, http.StatusOK, out)
	return nil
}

// ticket is the address that finishes signing in on the hop's site.
func ticket(hop access.Hop, personID string) string {
	hop.Person = personID
	return "https://" + hop.Host + "/_sh/signin/return?ticket=" + access.SealHop(hop, ticketLife)
}

// provider is one outside way to prove an email: Google, or a company's.
type provider struct{ id, secret, authURL, tokenURL, userURL string }

func (h *handler) provider(name string) (provider, bool) {
	c := h.a.Config
	switch {
	case name == "google" && c.GoogleClientID != "":
		return provider{c.GoogleClientID, c.GoogleClientSecret, c.GoogleAuthURL, c.GoogleTokenURL, c.GoogleUserURL}, true
	case name == "company" && c.CompanyClientID != "":
		return provider{c.CompanyClientID, c.CompanyClientSecret, c.CompanyAuthURL, c.CompanyTokenURL, c.CompanyUserURL}, true
	}
	return provider{}, false
}

func (h *handler) callback(name string) string {
	return "https://" + h.a.Config.AppDomain + "/v1/auth/oauth/" + name + "/callback"
}

// start is route 3: send the browser to the provider. A hop from a site
// rides along in the state cookie.
func (h *handler) start(w http.ResponseWriter, r *http.Request) error {
	name := r.PathValue("provider")
	p, ok := h.provider(name)
	if !ok {
		return web.NotFound
	}
	state := access.NewSecret("")
	access.SetCookie(w, r, "sh_signin", state+"."+r.URL.Query().Get("hop"), stateLife)
	q := url.Values{"client_id": {p.id}, "redirect_uri": {h.callback(name)}, "response_type": {"code"},
		"scope": {"openid email"}, "state": {state}}
	http.Redirect(w, r, p.authURL+"?"+q.Encode(), http.StatusFound)
	return nil
}

// finish is route 4: the provider's verified email picks the person.
func (h *handler) finish(w http.ResponseWriter, r *http.Request) error {
	name := r.PathValue("provider")
	p, ok := h.provider(name)
	if !ok {
		return web.NotFound
	}
	state, hop, _ := strings.Cut(access.Cookie(r, "sh_signin"), ".")
	access.SetCookie(w, r, "sh_signin", "", 0)
	if state == "" || subtle.ConstantTimeCompare([]byte(state), []byte(r.URL.Query().Get("state"))) != 1 {
		return errState
	}
	email, err := h.email(r, p, name, r.URL.Query().Get("code"))
	if err != nil {
		return err
	}
	who, err := h.a.People.Ensure(r.Context(), email)
	if err != nil {
		return err
	}
	if err := h.a.People.StartSession(w, r, who.ID); err != nil {
		return err
	}
	next := "/dashboard"
	if hop != "" {
		t, err := access.OpenHop(hop)
		if err != nil {
			return errHop
		}
		next = ticket(t, who.ID)
	}
	http.Redirect(w, r, next, http.StatusFound)
	return nil
}

// email trades the provider's code for the verified email address.
func (h *handler) email(r *http.Request, p provider, name, code string) (string, error) {
	refused := web.Refuse(http.StatusBadGateway, "provider_failed", "The sign-in provider did not confirm your email. Try again, or use an emailed code.")
	res, err := h.a.HTTP.PostForm(p.tokenURL, url.Values{"code": {code}, "client_id": {p.id}, "client_secret": {p.secret},
		"redirect_uri": {h.callback(name)}, "grant_type": {"authorization_code"}})
	if err != nil {
		return "", err
	}
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	err = json.NewDecoder(res.Body).Decode(&tok)
	res.Body.Close()
	if err != nil || res.StatusCode != http.StatusOK || tok.AccessToken == "" {
		return "", refused
	}
	req, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, p.userURL, nil)
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	if res, err = h.a.HTTP.Do(req); err != nil {
		return "", err
	}
	defer res.Body.Close()
	var u struct {
		Email    string `json:"email"`
		Verified bool   `json:"email_verified"`
	}
	if json.NewDecoder(res.Body).Decode(&u) != nil || res.StatusCode != http.StatusOK || !u.Verified {
		return "", refused
	}
	return normalEmail(u.Email)
}

// signOut is route 5: this browser, or with everywhere, every browser, every
// site and every connected app.
func (h *handler) signOut(w http.ResponseWriter, r *http.Request) error {
	c := web.CallerOf(r)
	if c.ID == "" {
		return errSignIn
	}
	var in struct{ Everywhere bool }
	if r.ContentLength != 0 {
		if err := web.Read(r, &in); err != nil {
			return err
		}
	}
	var err error
	if in.Everywhere {
		err = h.a.People.EndSignIns(r.Context(), c.ID)
	} else {
		_, err = h.a.DB.ExecContext(r.Context(), `DELETE FROM sessions WHERE id_hash = $1`, access.Hash(access.Cookie(r, "sh_session")))
	}
	if err != nil {
		return err
	}
	access.SetCookie(w, r, "sh_session", "", 0)
	web.JSON(w, http.StatusOK, map[string]bool{"signed_out": true})
	return nil
}
