package datatest

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	accountapp "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/accesshttp"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/server/servertest"
)

func TestConnectorPhotoGrant(t *testing.T) {
	for _, change := range []string{"access_expired", "disconnect", "sign_out_everywhere", "suspended", "user_deleted", "different_user", "different_client", "different_resource"} {
		t.Run(change, func(t *testing.T) {
			a := servertest.NewConnectorApp(t)
			owner := a.NewPerson(t, "photo-grant")
			client := a.RegisterClient(t, servertest.TestRedirect)
			access := a.Connect(t, owner, client, servertest.TestRedirect)["access_token"].(string)
			grant, err := accountsrepo.GetOAuthToken(context.Background(), a.Database, accesshttp.HashSecret(access))
			if err != nil {
				t.Fatal(err)
			}
			if r := a.Do(t, "POST", "/v1/sites/blog/files", servertest.JSONBody(map[string]any{"files": map[string]string{"index.html": "keep"}}), map[string]string{"X-API-Key": owner.Key}); r.Status != 201 {
				t.Fatal("create site failed")
			}
			mint := func(archive bool) *url.URL {
				t.Helper()
				_, out, failed := servertest.ToolResultOf(t, a.Rpc(t, access, "tools/call", map[string]any{"name": "create_upload_link", "arguments": map[string]any{"site": "blog", "archive": archive}}))
				if failed {
					t.Fatal("mint failed")
				}
				field, ttl := "photos_url", time.Hour
				if archive {
					field, ttl = "upload_url", 15*time.Minute
				}
				expires, err := time.Parse(time.RFC3339, out["expires_at"].(string))
				if err != nil || time.Until(expires) < ttl-time.Minute || time.Until(expires) > ttl {
					t.Fatal("wrong link lifetime")
				}
				u, err := url.Parse(out[field].(string))
				if err != nil {
					t.Fatal("invalid link URL")
				}
				return u
			}
			getLink, saveLink, archive := mint(false), mint(false), mint(true)
			exec := func(query string, args ...any) {
				t.Helper()
				if _, err := a.Database.Exec(query, args...); err != nil {
					t.Fatal(err)
				}
			}
			switch change {
			case "access_expired":
				exec(`UPDATE oauth_tokens SET expires_at=now()-interval '1 second' WHERE token_hash=$1`, accesshttp.HashSecret(access))
			case "disconnect":
				if r := a.Do(t, "DELETE", "/v1/me/connections/"+client, nil, map[string]string{"X-API-Key": owner.Key}); r.Status != 204 {
					t.Fatal("disconnect failed")
				}
			case "sign_out_everywhere":
				if r := a.Do(t, "POST", "/v1/me/api-key/rotate", nil, map[string]string{"X-API-Key": owner.Key}); r.Status != 200 {
					t.Fatal("sign out everywhere failed")
				}
			case "suspended":
				exec(`UPDATE users SET suspended_at=now() WHERE id=$1`, grant.UserID)
			case "user_deleted":
				exec(`DELETE FROM users WHERE id=$1`, grant.UserID)
			case "different_user":
				other := a.NewPerson(t, "photo-grant-other")
				u, err := accountapp.New(a.Database).GetUserByAPIKey(context.Background(), other.Key, 180*24*time.Hour)
				if err != nil {
					t.Fatal(err)
				}
				exec(`UPDATE oauth_grants SET user_id=$1 WHERE id=$2`, u.ID, grant.GrantID)
			case "different_client":
				exec(`UPDATE oauth_grants SET client_id=$1 WHERE id=$2`, a.RegisterClient(t, servertest.TestRedirect), grant.GrantID)
			case "different_resource":
				exec(`UPDATE oauth_grants SET resource=$1 WHERE id=$2`, a.Conn.Issuer, grant.GrantID)
			}
			want := 403
			if change == "access_expired" {
				want = 200
				if r := a.Do(t, "PUT", archive.Path, strings.NewReader("unused"), nil); r.Status != 403 || r.JSON(t)["code"] != "upload_link_signed_out" {
					t.Fatal("archive no longer rechecks the access token")
				}
			}
			if r := a.Do(t, "GET", "/v1/photo-links", nil, map[string]string{"X-Photo-Token": strings.TrimPrefix(getLink.Fragment, "t=")}); r.Status != want {
				t.Fatalf("photo lookup: got %d, want %d", r.Status, want)
			}
			body, ct := photoMultipart(t, []string{"grant.jpg"}, [][]byte{photoJPEG(t, 8, 6)})
			if r := a.Do(t, "POST", "/v1/photo-links", strings.NewReader(body), map[string]string{"X-Photo-Token": strings.TrimPrefix(saveLink.Fragment, "t="), "Content-Type": ct}); r.Status != want {
				t.Fatalf("photo save: got %d, want %d", r.Status, want)
			}
		})
	}
}
