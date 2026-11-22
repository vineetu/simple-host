package profile

import (
	"context"
	"regexp"
	"strings"
	"testing"

	. "github.com/vsriram/simple-host/host/access/featuretest"

	"github.com/vsriram/simple-host/host/access/auth"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	model "github.com/vsriram/simple-host/host/model"
)

var undoLinkRe = regexp.MustCompile(`/v1/me/email/undo#t=([0-9a-f]{48})`)

func TestEmailChangeRefusals(t *testing.T) {
	a, mb := NewAccountApp(t)
	p := a.NewPerson(t, "stay")
	other := a.NewPerson(t, "taken")
	host := "simple-host.test"
	key := p.Header()
	ask := func(addr string, hdr map[string]string) Resp {
		return a.At(t, "POST", host, "/v1/me/email", map[string]string{"email": addr}, hdr)
	}

	// Not the person's own key.
	if r := ask("x@example.org", map[string]string{"X-API-Key": a.Admin}); r.Status != 400 || r.Code() != "not_an_account_key" {
		t.Fatalf("admin key: %d %s", r.Status, r.Body)
	}
	if r := ask(p.Email, key); r.Status != 400 || r.Code() != "same_email" {
		t.Fatalf("same email: %d %s", r.Status, r.Body)
	}
	if r := ask("nope", key); r.Status != 400 {
		t.Fatalf("bad email: %d %s", r.Status, r.Body)
	}
	// Another account's address: the code goes out like any other (asking
	// reveals nothing); only verified codes learn it is taken.
	code, cur := AskEmailChange(t, a.Base, mb, key, p.Email, other.Email)
	r := a.At(t, "POST", host, "/v1/me/email/verify", map[string]string{"code": code, "current_code": cur}, key)
	if r.Status != 409 || r.Code() != "email_taken" {
		t.Fatalf("taken: %d %s", r.Status, r.Body)
	}
	var n int
	_ = a.Database.QueryRow(`SELECT count(*) FROM users WHERE username = $1`, p.Email).Scan(&n)
	if n != 1 {
		t.Fatal("account moved despite the address being taken")
	}

	// Nobody moves onto the reviewer account's address; the reviewer account's email is the operator's.
	rv := Uniq("reviewer") + "@example.org"
	a.Users.SetReviewerEmail(rv)
	if r := ask(strings.ToUpper(rv), key); r.Status != 400 || r.Code() != "reserved_email" {
		t.Fatalf("to the reviewer address: %d %s", r.Status, r.Body)
	}
	a.Users.SetReviewerEmail(p.Email)
	if r := ask("y@example.org", key); r.Status != 403 || r.Code() != "reviewer_account" {
		t.Fatalf("reviewer: %d %s", r.Status, r.Body)
	}
	a.Users.SetReviewerEmail("")

	// Preview, event and admin accounts, and a name that is not an address.
	a.Users.SetPreviewAccounts(map[string]bool{strings.ToLower(p.Email): true})
	if r := ask("y@example.org", key); r.Status != 403 || r.Code() != "preview_account" {
		t.Fatalf("preview account: %d %s", r.Status, r.Body)
	}
	a.Users.SetPreviewAccounts(nil)
	k, _ := auth.GenerateAPIKey()
	if err := accountsrepo.AddAPIKey(context.Background(), a.Database, p.ID, k, model.KeyNameEvent); err != nil {
		t.Fatal(err)
	}
	if err := accountsrepo.MarkEventAccount(context.Background(), a.Database, p.ID); err != nil {
		t.Fatal(err)
	}
	if r := ask("y@example.org", key); r.Status != 403 || r.Code() != "event_account" {
		t.Fatalf("event account: %d %s", r.Status, r.Body)
	}
	tk, _ := auth.GenerateAPIKey()
	if _, err := accountsrepo.CreateUser(context.Background(), a.Database, Uniq("team"), tk, false); err != nil {
		t.Fatal(err)
	}
	if r := ask("y@example.org", map[string]string{"X-API-Key": tk}); r.Status != 403 || r.Code() != "no_current_email" {
		t.Fatalf("no address to confirm from: %d %s", r.Status, r.Body)
	}
	ak, _ := auth.GenerateAPIKey()
	if _, err := accountsrepo.CreateUser(context.Background(), a.Database, Uniq("adm")+"@example.org", ak, true); err != nil {
		t.Fatal(err)
	}
	if r := ask("y@example.org", map[string]string{"X-API-Key": ak}); r.Status != 403 || r.Code() != "admin_account" {
		t.Fatalf("admin account: %d %s", r.Status, r.Body)
	}
}

// The owner app lists linked Google/GitHub sign-ins and can unlink one;
// own key only, own sign-ins only.
func TestIdentitiesListAndUnlink(t *testing.T) {
	a, _ := NewAccountApp(t)
	p, q := a.NewPerson(t, "linked"), a.NewPerson(t, "stranger")
	host := "simple-host.test"
	var mine, theirs string
	if err := a.Database.QueryRow(`INSERT INTO oauth_identities (user_id, provider, provider_user_id, email, email_verified) VALUES ($1, 'google', $2, 'p@oldco.example', true) RETURNING id`, p.ID, Uniq("g")).Scan(&mine); err != nil {
		t.Fatal(err)
	}
	if err := a.Database.QueryRow(`INSERT INTO oauth_identities (user_id, provider, provider_user_id, email, email_verified) VALUES ($1, 'google', $2, 'q@example', true) RETURNING id`, q.ID, Uniq("g")).Scan(&theirs); err != nil {
		t.Fatal(err)
	}
	key := p.Header()
	r := a.At(t, "GET", host, "/v1/me/identities", nil, key)
	if r.Status != 200 || !strings.Contains(string(r.Body), `"id":"`+mine+`"`) || strings.Contains(string(r.Body), theirs) {
		t.Fatalf("list: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "DELETE", host, "/v1/me/identities/"+theirs, nil, key); r.Status != 404 {
		t.Fatalf("someone else's sign-in: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "DELETE", host, "/v1/me/identities/"+mine, nil, map[string]string{"X-API-Key": a.Admin}); r.Status != 400 {
		t.Fatalf("admin key: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "DELETE", host, "/v1/me/identities/"+mine, nil, key); r.Status != 204 {
		t.Fatalf("unlink: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", host, "/v1/me/identities", nil, key); strings.Contains(string(r.Body), mine) {
		t.Fatalf("still listed: %s", r.Body)
	}
}
