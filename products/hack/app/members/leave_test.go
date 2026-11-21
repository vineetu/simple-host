package members

import (
	"errors"
	"testing"

	model "github.com/vsriram/simple-host/products/hack/model"
)

func TestCheckLeaveRequest(t *testing.T) {
	others := []model.OtherOrganiser{{UserID: "u2", DisplayName: "Bo"}}
	for _, tc := range []struct {
		leaveCase string
		req       LeaveRequest
		code      string
	}{
		{"participant", LeaveRequest{}, ""},
		{"participant", LeaveRequest{SuccessorID: "u2"}, "leave_state_changed"},
		{"co_organiser", LeaveRequest{Close: "end"}, "leave_state_changed"},
		{"creator", LeaveRequest{SuccessorID: "u2"}, ""},
		{"creator", LeaveRequest{}, "successor_required"},
		{"creator", LeaveRequest{SuccessorID: "u9"}, "invalid_successor"},
		{"creator", LeaveRequest{Close: "end"}, "leave_state_changed"},
		{"last_organiser", LeaveRequest{Close: "delete"}, ""},
		{"last_organiser", LeaveRequest{}, "last_organiser"},
		{"last_organiser", LeaveRequest{SuccessorID: "u2", Close: "end"}, "leave_state_changed"},
	} {
		successor, err := checkLeaveRequest(tc.leaveCase, tc.req, others)
		code := ""
		if e := (*Error)(nil); errors.As(err, &e) {
			code = e.Code
		}
		if code != tc.code {
			t.Errorf("%s %+v: code %q, want %q", tc.leaveCase, tc.req, code, tc.code)
		}
		if tc.code == "" && tc.leaveCase == "creator" && successor.DisplayName != "Bo" {
			t.Errorf("creator successor = %+v", successor)
		}
	}
}

func TestRejoinFor(t *testing.T) {
	for _, tc := range []struct{ leaveCase, stage, want string }{
		{"participant", "open", "join"}, {"applicant", "building", "join"}, {"participant", "judging", "none"},
		{"judge", "judging", "judge_link"}, {"judge", "results", "none"},
		{"creator", "archived", "organiser_invite"}, {"co_organiser", "open", "organiser_invite"},
		{"last_organiser", "open", "none"},
	} {
		if got := rejoinFor(tc.leaveCase, tc.stage); got != tc.want {
			t.Errorf("rejoinFor(%s, %s) = %s, want %s", tc.leaveCase, tc.stage, got, tc.want)
		}
	}
}
