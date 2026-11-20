package sites

import (
	"context"
	"database/sql"
	"testing"

	"github.com/vsriram/simple-host/host/model"
	repo "github.com/vsriram/simple-host/host/repository/sites"
)

type groupGateFunc func(context.Context, *sql.Tx, *model.User, string) *GateError

func (f groupGateFunc) Check(ctx context.Context, tx *sql.Tx, user *model.User, name string) *GateError {
	return f(ctx, tx, user, name)
}

func (f groupGateFunc) Suspension(context.Context, *sql.Tx, repo.Site) (string, string, error) {
	return "", "", nil
}

func TestSuspendHeldSiteRejectsEmptyPrefix(t *testing.T) {
	for _, on := range []bool{false, true} {
		// No dependencies: rejection must precede locks, SQL and callbacks.
		svc := New(nil, nil, nil, 0, Deps{})
		if err := svc.SuspendHeldSite(context.Background(), HeldSiteSuspension{On: on, Reason: "operator reason", Also: func(*sql.Tx) error {
			t.Fatal("empty prefix reached product update")
			return nil
		}}); err == nil {
			t.Fatal("empty prefix accepted")
		}
	}
}

func TestCreateGateDispatch(t *testing.T) {
	for _, identity := range []string{"personal", "member", "editor"} {
		t.Run(identity, func(t *testing.T) {
			user := &model.User{ID: "holder"}
			if identity == "member" {
				user.Member = &model.MemberIdentity{PersonID: "person"}
			}
			if identity == "editor" {
				user.Member = &model.MemberIdentity{PersonID: "person", Site: "site"}
			}
			svc := New(nil, nil, nil, 0, Deps{})
			if err := svc.createGate(context.Background(), nil, user, "site"); err != nil {
				t.Fatalf("nil hook: %v", err)
			}
			tx := new(sql.Tx)
			calls := 0
			refusal := &GateError{Code: "event_not_found", Message: "event not found"}
			svc.deps.GroupGate = groupGateFunc(func(ctx context.Context, got *sql.Tx, u *model.User, name string) *GateError {
				calls++
				if got != tx || u != user || name != "site" {
					t.Fatal("gate did not receive caller's transaction and identity")
				}
				return refusal
			})
			got := svc.createGate(context.Background(), tx, user, "site")
			if identity == "personal" {
				if calls != 0 || got != nil {
					t.Fatal("personal user reached product gate")
				}
			} else if calls != 1 || got != refusal {
				t.Fatalf("calls=%d refusal=%v", calls, got)
			}
		})
	}
}
