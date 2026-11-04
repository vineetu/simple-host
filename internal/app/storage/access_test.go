package storage

import (
	"testing"

	rules "github.com/vsriram/simple-host/internal/rules/storage"
)

func TestAllowMatrixDecisions(t *testing.T) {
	s := New(nil, Deps{})
	tests := []struct {
		name                        string
		call                        Call
		action, value, filter, code string
		allowed                     bool
	}{
		{name: "owner", call: Call{Owner: true}, action: "delete", value: rules.WhoNobody, allowed: true},
		{name: "visitor AI edit", call: Call{VisitorAI: true, VisitorID: "v"}, action: "edit", value: rules.WhoAnyone, filter: "v", allowed: true},
		{name: "own visitor", call: Call{VisitorID: "v", ActiveChecked: true}, action: "read", value: rules.WhoOwn, filter: "v", allowed: true},
		{name: "sign in", action: "read", value: rules.WhoOwn, code: "sign_in_required"},
		{name: "add only", call: Call{CurrentMatrix: rules.AccessMatrix{Add: rules.WhoAnyone}}, action: "edit", value: rules.WhoOwner, code: "add_only"},
		{name: "checked", call: Call{SpamCheck: "ok"}, action: "add", value: rules.WhoChecked, allowed: true},
		{name: "checked required", action: "add", value: rules.WhoChecked, code: "spam_check_required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter, err := s.Allow(&tt.call, tt.action, tt.value)
			allowed := err == nil
			var errs []*Error
			if err != nil {
				e := err.(*Error)
				allowed, errs = e.Allowed, e.Refusals
			}
			if filter != tt.filter || allowed != tt.allowed {
				t.Fatalf("got filter %q, allowed %v", filter, allowed)
			}
			if tt.code == "" && len(errs) != 0 || tt.code != "" && (len(errs) != 1 || errs[0].Code != tt.code) {
				t.Fatalf("unexpected errors: %v", errs)
			}
			if tt.name == "checked" && !tt.call.CheckedPost {
				t.Fatal("checked post not recorded")
			}
		})
	}
}
