package accesstest

// Upload and photo links (create_upload_link, PUT /v1/uploads/{token},
// /v1/photo-links) are the connector's: a link carries no key, works for one
// site, and ends with the sign-in it was made from. Sites' routes it forwards
// to are the fixture's stand-ins, which answer who the forwarded key acts as.

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	accountapp "github.com/vsriram/simple-host/host/access"

	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
)

// mintLink calls create_upload_link for site with an OAuth token (or, with
// key set, with an API key on /mcp) and returns the link's token and expiry.
func mintLink(t *testing.T, a *App, token, key, site string, archive bool) (string, time.Time) {
	t.Helper()
	params := map[string]any{"name": "create_upload_link", "arguments": map[string]any{"site": site, "archive": archive}}
	var r Resp
	if key != "" {
		r = a.Do(t, "POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": params},
			map[string]string{"X-API-Key": key, "Content-Type": "application/json"})
	} else {
		r = a.Rpc(t, token, "tools/call", params)
	}
	_, out, failed := ToolResultOf(t, r)
	if failed {
		t.Fatalf("mint: %d %s", r.Status, r.Body)
	}
	field := "photos_url"
	if archive {
		field = "upload_url"
	}
	u, err := url.Parse(out[field].(string))
	if err != nil {
		t.Fatal(err)
	}
	expires, err := time.Parse(time.RFC3339, out["expires_at"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if archive {
		return strings.TrimPrefix(u.Path, "/v1/uploads/"), expires
	}
	return strings.TrimPrefix(u.Fragment, "t="), expires
}

func putUpload(t *testing.T, a *App, token string) Resp {
	t.Helper()
	return a.Do(t, "PUT", "/v1/uploads/"+token, "archive", map[string]string{"Content-Type": "application/gzip"})
}

func photoLink(t *testing.T, a *App, method, token, query string) Resp {
	t.Helper()
	var body any
	if method == "POST" {
		body = "photos"
	}
	return a.Do(t, method, "/v1/photo-links"+query, body, map[string]string{"X-Photo-Token": token, "Origin": a.Srv.URL, "Content-Type": "multipart/form-data; boundary=x"})
}

// An archive link deploys once, as its maker, for 15 minutes; it ends when
// the connection is removed or the key it was made with is revoked.
func TestUploadLinkSingleUseAndEnds(t *testing.T) {
	a := NewApp(t)
	owner := a.NewPerson(t, "upload-owner")
	client := a.RegisterClient(t, TestRedirect)
	token := a.Connect(t, owner, client, TestRedirect)["access_token"].(string)

	link, expires := mintLink(t, a, token, "", "archive", true)
	if left := time.Until(expires); left > 15*time.Minute || left < 14*time.Minute {
		t.Fatalf("archive link lifetime: %v", left)
	}
	if r := putUpload(t, a, link); r.Status != http.StatusCreated || r.JSON(t)["user"] != owner.ID || r.JSON(t)["site"] != "archive" {
		t.Fatalf("upload: %d %s", r.Status, r.Body)
	}
	if r := putUpload(t, a, link); r.Status != 404 || r.Code() != "upload_link_invalid" {
		t.Fatalf("reuse: %d %s", r.Status, r.Body)
	}

	r := a.Do(t, "POST", "/v1/me/keys", map[string]any{"name": "upload", "scope": "deploy"}, owner.Header())
	if r.Status != http.StatusCreated {
		t.Fatalf("deploy key: %d %s", r.Status, r.Body)
	}
	keyID, deployKey := r.JSON(t)["id"].(string), r.JSON(t)["api_key"].(string)
	if r := putUpload(t, a, first(mintLink(t, a, "", deployKey, "archive", true))); r.Status != http.StatusCreated {
		t.Fatalf("deploy-only key's link: %d %s", r.Status, r.Body)
	}

	link = first(mintLink(t, a, token, "", "archive", true))
	if r := a.Do(t, "DELETE", "/v1/me/connections/"+client, nil, owner.Header()); r.Status != 204 {
		t.Fatalf("disconnect: %d %s", r.Status, r.Body)
	}
	if r := putUpload(t, a, link); r.Status != 403 || r.Code() != "upload_link_signed_out" {
		t.Fatalf("after disconnect: %d %s", r.Status, r.Body)
	}
	link = first(mintLink(t, a, "", deployKey, "archive", true))
	if r := a.Do(t, "DELETE", "/v1/me/keys/"+keyID, nil, owner.Header()); r.Status != 204 {
		t.Fatalf("revoke: %d %s", r.Status, r.Body)
	}
	if r := putUpload(t, a, link); r.Status != 403 || r.Code() != "upload_link_signed_out" {
		t.Fatalf("after key revoke: %d %s", r.Status, r.Body)
	}
}

func first(s string, _ time.Time) string { return s }

// A photo link shows only its site, saves to that site alone as its maker
// (no query can move it), and is spent after ten saves.
func TestPhotoLinkFixedSiteAndSpent(t *testing.T) {
	a := NewApp(t)
	owner := a.NewPerson(t, "photo-link")
	token := a.Connect(t, owner, a.RegisterClient(t, TestRedirect), TestRedirect)["access_token"].(string)
	link, expires := mintLink(t, a, token, "", "blog", false)
	if left := time.Until(expires); left > time.Hour || left < 59*time.Minute {
		t.Fatalf("photo link lifetime: %v", left)
	}
	if r := photoLink(t, a, "GET", link, ""); r.Status != 200 || len(r.JSON(t)) != 1 || r.JSON(t)["site"] != "blog" {
		t.Fatalf("lookup: %d %s", r.Status, r.Body)
	}
	for i := 0; i < 10; i++ {
		r := photoLink(t, a, "POST", link, "?site=other&path=/v1/me")
		if r.Status != 200 || r.JSON(t)["site"] != "blog" || r.JSON(t)["user"] != owner.ID {
			t.Fatalf("save %d: %d %s", i, r.Status, r.Body)
		}
	}
	if r := photoLink(t, a, "POST", link, ""); r.Status != 404 || r.Code() != "photo_link_expired" {
		t.Fatalf("eleventh save: %d %s", r.Status, r.Body)
	}
	if r := photoLink(t, a, "GET", "not-a-link", ""); r.Status != 404 {
		t.Fatalf("unknown token: %d %s", r.Status, r.Body)
	}
}

// A photo link made with an API key ends when that key expires.
func TestPhotoLinkEndsWithKeyExpiry(t *testing.T) {
	a := NewApp(t)
	owner := a.NewPerson(t, "photo-expiry")
	expiry := time.Now().Add(5 * time.Minute).UTC().Truncate(time.Second)
	if _, err := a.Database.Exec(`UPDATE api_keys SET expires_at=$1 WHERE key_hash=$2`, expiry, accountsrepo.HashAPIKey(owner.Key)); err != nil {
		t.Fatal(err)
	}
	if _, got := mintLink(t, a, "", owner.Key, "blog", false); !got.Equal(expiry) {
		t.Fatalf("API-key photo link expires %v, key %v", got, expiry)
	}
}

// A photo link ends with the connection grant behind it: disconnected,
// signed out everywhere, account suspended or deleted, or the grant moved
// to another person. An expired access token alone does not end it (the
// grant still stands), but it does end an archive link.
func TestPhotoLinkEndsWithItsGrant(t *testing.T) {
	a := NewApp(t)
	for _, change := range []string{"access_expired", "disconnect", "sign_out_everywhere", "suspended", "user_deleted", "different_user"} {
		t.Run(change, func(t *testing.T) {
			owner := a.NewPerson(t, "photo-grant")
			client := a.RegisterClient(t, TestRedirect)
			access := a.Connect(t, owner, client, TestRedirect)["access_token"].(string)
			grant, err := accountsrepo.GetOAuthToken(context.Background(), a.Database, accountapp.HashSecret(access))
			if err != nil {
				t.Fatal(err)
			}
			getLink, saveLink := first(mintLink(t, a, access, "", "blog", false)), first(mintLink(t, a, access, "", "blog", false))
			archive := first(mintLink(t, a, access, "", "blog", true))
			exec := func(query string, args ...any) {
				t.Helper()
				if _, err := a.Database.Exec(query, args...); err != nil {
					t.Fatal(err)
				}
			}
			switch change {
			case "access_expired":
				exec(`UPDATE oauth_tokens SET expires_at=now()-interval '1 second' WHERE token_hash=$1`, accountapp.HashSecret(access))
			case "disconnect":
				if r := a.Do(t, "DELETE", "/v1/me/connections/"+client, nil, owner.Header()); r.Status != 204 {
					t.Fatalf("disconnect: %d %s", r.Status, r.Body)
				}
			case "sign_out_everywhere":
				if r := a.Do(t, "POST", "/v1/me/api-key/rotate", nil, owner.Header()); r.Status != 200 {
					t.Fatalf("sign out everywhere: %d %s", r.Status, r.Body)
				}
			case "suspended":
				exec(`UPDATE users SET suspended_at=now() WHERE id=$1`, grant.UserID)
			case "user_deleted":
				exec(`DELETE FROM users WHERE id=$1`, grant.UserID)
			case "different_user":
				exec(`UPDATE oauth_grants SET user_id=$1 WHERE id=$2`, a.NewPerson(t, "photo-grant-other").ID, grant.GrantID)
			}
			want := 403
			if change == "access_expired" {
				want = 200
				if r := putUpload(t, a, archive); r.Status != 403 || r.Code() != "upload_link_signed_out" {
					t.Fatalf("archive after access expiry: %d %s", r.Status, r.Body)
				}
			}
			if r := photoLink(t, a, "GET", getLink, ""); r.Status != want {
				t.Fatalf("lookup: %d %s (want %d)", r.Status, r.Body, want)
			}
			if r := photoLink(t, a, "POST", saveLink, ""); r.Status != want {
				t.Fatalf("save: %d %s (want %d)", r.Status, r.Body, want)
			}
		})
	}
}
