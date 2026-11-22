package ops

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"log"

	"github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/data/notify"
	"github.com/vsriram/simple-host/host/model"
)

// EndSiteSignIn signs visitor session sess out of its site (sess is zero
// when the browser holds none). apex is the account apex's base URL, ""
// without one: then the session is simply deleted. With one, a session that
// is not the company sign-in's is deleted too, and the company sign-in is
// revoked (access.Service.RevokeVisitorSSO deletes the dashboard key that
// cascades to every sibling session of its family, on whichever sites),
// with the family's push devices; the answer is the one-use apex address
// that finishes the sign-out and then returns to back.
func EndSiteSignIn(ctx context.Context, database *sql.DB, sess model.VisitorSession, apex, back string) (string, error) {
	accounts := access.New(database)
	if len(sess.ID) > 0 && (!sess.SSOPassHash.Valid || apex == "") {
		if err := accounts.DeleteVisitorSession(ctx, sess.ID); err != nil {
			return "", err
		}
	}
	if apex == "" {
		return "", nil
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b)
	if err := accounts.RevokeVisitorSSO(ctx, sess, token, back); err != nil {
		return "", err
	}
	// Push subscriptions those sessions created follow the same family
	// scope (notify.PushFamily), so this reaches every site's push rows too.
	if family := notify.PushFamily(sess); family != "" {
		if n, err := notify.DeletePushSubscriptionsForFamily(ctx, database, family); err != nil {
			log.Printf("web push: delete on SSO revoke: %v", err)
		} else if n > 0 {
			log.Printf("web push: deleted %d subscription(s) on SSO revoke", n)
		}
	}
	return apex + "/v1/sso/signed-out?token=" + token, nil
}

// CompleteSSOSignout finishes signing the company sign-in out on the apex
// (access.Service.CompleteSSOSignout) and deletes the push subscriptions of
// the browser's sign-in family (passHash, the pass cookie's hash, which is
// what push_family holds), so every site that family's sign-in reached
// stops pushing. back is where the browser returns.
func CompleteSSOSignout(ctx context.Context, database *sql.DB, token, passHash, formHash string) (string, error) {
	back, err := access.New(database).CompleteSSOSignout(ctx, token, passHash, formHash)
	if err != nil {
		return "", err
	}
	if n, err := notify.DeletePushSubscriptionsForFamily(ctx, database, passHash); err != nil {
		log.Printf("web push: delete on SSO sign-out: %v", err)
	} else if n > 0 {
		log.Printf("web push: deleted %d subscription(s) on SSO sign-out", n)
	}
	return back, nil
}
