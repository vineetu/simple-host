package sites

import (
	"context"
	"database/sql"
	"testing"

	"github.com/vsriram/simple-host/internal/model"
)

type groupGateFunc func(context.Context, *sql.Tx, *model.User, string) *GateError

func (f groupGateFunc) Check(ctx context.Context, tx *sql.Tx, user *model.User, name string) *GateError {
	return f(ctx, tx, user, name)
}

func TestCreateGateDispatch(t *testing.T) {
	for _, identity := range []string{"personal", "team", "event"} {
		t.Run(identity, func(t *testing.T) {
			user := &model.User{ID: "holder"}
			if identity == "team" {
				user.Team = &model.TeamIdentity{}
			}
			if identity == "event" {
				user.EventWebsite = &model.EventWebsiteIdentity{}
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
