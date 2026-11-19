package servertest

import (
	"context"
	"testing"

	accountapp "github.com/vsriram/simple-host/host/app/accounts"
	recording "github.com/vsriram/simple-host/host/testdb"
	"github.com/vsriram/simple-host/products/hack/app/members"
	"github.com/vsriram/simple-host/products/hack/testdb"
)

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
	var kind string
	if err := db.QueryRow(`SELECT kind FROM site_storage_resources WHERE site_id=$1 AND name='settings'`, keySite).Scan(&kind); err != nil || kind != "kv" {
		t.Fatalf("holding-account data=%q err=%v", kind, err)
	}
}
