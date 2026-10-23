package handler

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/db"
)

func TestConnectorPhotoGrant(t *testing.T) {
	for _, change := range []string{"access_expired", "disconnect", "sign_out_everywhere", "suspended", "user_deleted", "different_user", "different_client", "different_resource"} {
		t.Run(change, func(t *testing.T) {
			a := newConnectorApp(t)
			owner := a.newPerson(t, "photo-grant")
			client := a.registerClient(t, testRedirect)
			access := a.connect(t, owner, client, testRedirect)["access_token"].(string)
			grant, err := db.GetOAuthToken(context.Background(), a.database, hashSecret(access))
			if err != nil {
				t.Fatal(err)
			}
			if r := a.do(t, "POST", "/v1/sites/blog/files", jsonBody(map[string]any{"files": map[string]string{"index.html": "keep"}}), map[string]string{"X-API-Key": owner.key}); r.status != 201 {
				t.Fatal("create site failed")
			}
			mint := func(archive bool) *url.URL {
				t.Helper()
				_, out, failed := toolResultOf(t, a.rpc(t, access, "tools/call", map[string]any{"name": "create_upload_link", "arguments": map[string]any{"site": "blog", "archive": archive}}))
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
				if _, err := a.database.Exec(query, args...); err != nil {
					t.Fatal(err)
				}
			}
			switch change {
			case "access_expired":
				exec(`UPDATE oauth_tokens SET expires_at=now()-interval '1 second' WHERE token_hash=$1`, hashSecret(access))
			case "disconnect":
				if r := a.do(t, "DELETE", "/v1/me/connections/"+client, nil, map[string]string{"X-API-Key": owner.key}); r.status != 204 {
					t.Fatal("disconnect failed")
				}
			case "sign_out_everywhere":
				if r := a.do(t, "POST", "/v1/me/api-key/rotate", nil, map[string]string{"X-API-Key": owner.key}); r.status != 200 {
					t.Fatal("sign out everywhere failed")
				}
			case "suspended":
				exec(`UPDATE users SET suspended_at=now() WHERE id=$1`, grant.UserID)
			case "user_deleted":
				exec(`DELETE FROM users WHERE id=$1`, grant.UserID)
			case "different_user":
				other := a.newPerson(t, "photo-grant-other")
				u, err := db.GetUserByAPIKey(context.Background(), a.database, other.key)
				if err != nil {
					t.Fatal(err)
				}
				exec(`UPDATE oauth_grants SET user_id=$1 WHERE id=$2`, u.ID, grant.GrantID)
			case "different_client":
				exec(`UPDATE oauth_grants SET client_id=$1 WHERE id=$2`, a.registerClient(t, testRedirect), grant.GrantID)
			case "different_resource":
				exec(`UPDATE oauth_grants SET resource=$1 WHERE id=$2`, a.conn.issuer, grant.GrantID)
			}
			want := 403
			if change == "access_expired" {
				want = 200
				if r := a.do(t, "PUT", archive.Path, strings.NewReader("unused"), nil); r.status != 403 || r.json(t)["code"] != "upload_link_signed_out" {
					t.Fatal("archive no longer rechecks the access token")
				}
			}
			if r := a.do(t, "GET", "/v1/photo-links", nil, map[string]string{"X-Photo-Token": strings.TrimPrefix(getLink.Fragment, "t=")}); r.status != want {
				t.Fatalf("photo lookup: got %d, want %d", r.status, want)
			}
			body, ct := photoMultipart(t, []string{"grant.jpg"}, [][]byte{photoJPEG(t, 8, 6)})
			if r := a.do(t, "POST", "/v1/photo-links", strings.NewReader(body), map[string]string{"X-Photo-Token": strings.TrimPrefix(saveLink.Fragment, "t="), "Content-Type": ct}); r.status != want {
				t.Fatalf("photo save: got %d, want %d", r.status, want)
			}
		})
	}
}
