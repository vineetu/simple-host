package servertest

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	accountapp "github.com/vsriram/simple-host/host/app/accounts"
	recording "github.com/vsriram/simple-host/host/testdb"
	"github.com/vsriram/simple-host/products/hack/app/members"
	"github.com/vsriram/simple-host/products/hack/testdb"
)

// Recorded before D2-S8 on the seeded fixture. Roles distinguish every statement,
// including locks and transaction boundaries, without coupling the oracle to SQL formatting.
func erasureRole(t *testing.T, query string) string {
	t.Helper()
	q := strings.Join(strings.Fields(query), " ")
	for _, role := range []struct{ match, name string }{
		{"BEGIN", "begin"}, {"COMMIT", "commit"}, {"ROLLBACK", "rollback"},
		{"SELECT id, username, handle, COALESCE(is_admin", "lock-account"},
		{"SELECT count(*) FROM event_domains", "check-hostnames"},
		{"SELECT count(*) FROM sites WHERE", "check-taken-down-sites"},
		{"SELECT EXISTS (SELECT 1 FROM events WHERE account_id", "check-holding-account"},
		{"SELECT EXISTS (SELECT 1 FROM event_members m JOIN", "check-running-event"},
		{"UPDATE events e SET stage", "end-taken-down-events"},
		{"UPDATE events e SET organiser_name", "clear-organiser-contact"},
		{"DELETE FROM event_teams t USING events e", "tidy-single-member-teams"},
		{"SELECT count(*) FROM (SELECT 1 FROM collection_settings", "lock-collection-settings"},
		{"SELECT count(*) FROM (SELECT 1 FROM collection_items", "lock-collection-items"},
		{"SELECT count(*) FROM (SELECT 1 FROM sites", "lock-sites"},
		{"SELECT id, name FROM sites", "list-sites"},
		{"SELECT handle FROM handle_aliases", "list-aliases"},
		{"SELECT suffix FROM address_families", "list-address-families"},
		{"SELECT team_id FROM event_members", "before-admin-teams"},
		{"INSERT INTO handle_aliases", "retire-handle"},
		{"DELETE FROM collection_items", "delete-submissions"},
		{"UPDATE data_history", "clear-history-email"},
		{"UPDATE handle_aliases", "retire-aliases"},
		{"UPDATE legacy_hostnames", "retire-hostnames"},
		{"DELETE FROM auth_tokens", "delete-signin-codes"},
		{"DELETE FROM sites", "delete-sites"},
		{"DELETE FROM event_checklist_acknowledgements", "delete-product-rows"},
		{"DELETE FROM users", "delete-account"},
		{"SELECT e.account_id FROM event_teams", "after-team-account"},
		{"DELETE FROM event_teams", "after-delete-empty-team"},
		{"SELECT EXISTS(SELECT 1 FROM event_teams", "after-team-exists"},
		{"SELECT COUNT(*) FROM event_members", "after-member-count"},
		{"SELECT count(*) FROM (SELECT 1 FROM event_members", "after-lock-members"},
		{"SELECT id, event_id, slug, name, code", "after-lock-team"},
		{"SELECT EXISTS (SELECT 1 FROM event_members WHERE team_id", "after-check-admin"},
		{"INSERT INTO event_notices", "after-notify-successor"},
		{"UPDATE event_members SET team_admin", "after-promote-admin"},
		{"SELECT name, custom_domain", "retire-site-names"},
		{"SELECT hostname FROM legacy_hostnames", "list-retired-site-names"},
	} {
		if strings.HasPrefix(q, role.match) {
			return role.name
		}
	}
	t.Fatalf("unclassified erasure statement: %s", q)
	return ""
}

func TestAccountErasureStatementOrder(t *testing.T) {
	for _, name := range []string{"last-member", "only-admin", "running-organiser", "holding-account", "operator-delete", "event-hostname"} {
		t.Run(name, func(t *testing.T) {
			db, rec := recording.OpenRecording(testdb.Fresh(t))
			defer db.Close()
			seedGroupKeys(t, db)
			accountapp.SetGroupHooks(accountapp.GroupHooks{Erasure: members.AccountErasure{}})
			t.Cleanup(func() { accountapp.SetGroupHooks(accountapp.GroupHooks{}) })
			exec := func(q string, args ...any) {
				t.Helper()
				if _, err := db.Exec(q, args...); err != nil {
					t.Fatal(err)
				}
			}
			// Keep personal site retirement out of these membership fixtures.
			exec(`DELETE FROM sites WHERE user_id IN ($1,$2)`, keyMember, "00000000-0000-4000-8000-000000000002")
			id, confirm, code := keyMember, "seed-one", ""
			if name == "only-admin" {
				exec(`UPDATE event_members SET team_id=$1,team_admin=false WHERE user_id=$2`, keyTeam, "00000000-0000-4000-8000-000000000002")
			}
			if name == "running-organiser" {
				id, confirm, code = keyOrganiser, "seed-organiser", "organises_events"
			}
			if name == "holding-account" {
				id, confirm, code = keyHolder, "seed-rehearsal", "event_account"
			}
			if name == "event-hostname" {
				code = "event_hostnames"
				exec(`INSERT INTO event_domains (name,domain,user_id,ip,expires_at) VALUES ('erasure','example.com',$1,'127.0.0.1',now()+interval '1 day')`, id)
			}
			rec.Reset()
			_, err := accountapp.New(db).DeleteAccount(context.Background(), accountapp.DeleteAccountInput{ID: id, Confirm: confirm, Operator: name == "operator-delete"})
			if code == "" && err != nil {
				t.Fatal(err)
			}
			if code != "" {
				var e *accountapp.LifecycleError
				if !errors.As(err, &e) || e.Code != code || e.Status != 409 {
					t.Fatalf("refusal: %v", err)
				}
			}
			if code == "event_hostnames" && err.Error() != "this account still holds event hostnames; release them first so their DNS records are removed" {
				t.Fatal(err)
			}
			var roles []string
			for _, q := range rec.Statements() {
				roles = append(roles, erasureRole(t, q))
			}
			path := filepath.Join("testdata", "account-erasure", name+".json")
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var want []string
			if err = json.Unmarshal(b, &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(roles, want) {
				t.Fatalf("statement roles:\ngot %v\nwant %v", roles, want)
			}
		})
	}
}

func TestAccountErasureSeededRehearsal(t *testing.T) {
	db, _ := recording.OpenRecording(testdb.Fresh(t))
	defer db.Close()
	seedGroupKeys(t, db)
	accountapp.SetGroupHooks(accountapp.GroupHooks{Erasure: members.AccountErasure{}})
	t.Cleanup(func() { accountapp.SetGroupHooks(accountapp.GroupHooks{}) })
	out, err := accountapp.New(db).DeleteAccount(context.Background(), accountapp.DeleteAccountInput{ID: keyMember, Confirm: "seed-one"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Erased.Sites) != 2 {
		t.Fatalf("erased sites=%d", len(out.Erased.Sites))
	}
	for _, q := range []string{
		`SELECT count(*) FROM users WHERE id=$1`,
		`SELECT count(*) FROM sites WHERE user_id=$1`,
		`SELECT count(*) FROM event_members WHERE user_id=$1`,
		`SELECT count(*) FROM oauth_grants WHERE user_id=$1`,
		`SELECT count(*) FROM collection_items WHERE submitted_by=$1`,
		`SELECT count(*) FROM event_checklist_acknowledgements WHERE subject_id=$1`,
	} {
		var n int
		if err := db.QueryRow(q, keyMember).Scan(&n); err != nil || n != 0 {
			t.Fatalf("erasure rows=%d err=%v for %s", n, err, q)
		}
	}
	var others int
	if err := db.QueryRow(`SELECT count(*) FROM users WHERE id IN ($1,$2,$3)`, keyHolder, keyOrganiser, "00000000-0000-4000-8000-000000000002").Scan(&others); err != nil || others != 3 {
		t.Fatalf("other accounts=%d err=%v", others, err)
	}
	var value string
	if err := db.QueryRow(`SELECT value::text FROM site_storage_kv WHERE site_id=$1 AND resource_name='settings' AND key='theme'`, keySite).Scan(&value); err != nil || value != `"teal"` {
		t.Fatalf("holding-account data=%q err=%v", value, err)
	}
}
