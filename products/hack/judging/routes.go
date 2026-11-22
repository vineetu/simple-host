package judging

import (
	"net/http"

	"github.com/vsriram/simple-host/products/hack/core"
)

// Register adds the judging routes.
func Register(mux *http.ServeMux, c *core.Hack, authMW func(http.Handler) http.Handler) {
	h := &handler{c}
	wrap := func(fn http.HandlerFunc) http.Handler { return authMW(fn) }
	mux.Handle("GET /v1/hack/events/{slug}/rubric", wrap(h.getRubric))
	mux.Handle("PUT /v1/hack/events/{slug}/rubric", wrap(h.putRubric))
	mux.Handle("GET /v1/hack/events/{slug}/judging/settings", wrap(h.getJudgingSettings))
	mux.Handle("PATCH /v1/hack/events/{slug}/judging/settings", wrap(h.patchJudgingSettings))
	mux.Handle("POST /v1/hack/events/{slug}/assignments/generate", wrap(h.generateAssignments))
	mux.Handle("GET /v1/hack/events/{slug}/conflicts", wrap(h.listConflicts))
	mux.Handle("POST /v1/hack/events/{slug}/conflicts", wrap(h.declareConflict))
	mux.Handle("DELETE /v1/hack/events/{slug}/conflicts/{team_id}", wrap(h.deleteConflict))
	mux.Handle("GET /v1/hack/events/{slug}/judging/dashboard", wrap(h.judgingDashboard))
	mux.Handle("DELETE /v1/hack/events/{slug}/judging/former-judges/{user_id}", wrap(h.deleteFormerJudgeScores))
	mux.Handle("POST /v1/hack/events/{slug}/judging/lock", wrap(h.judgingLockRemoved))
	mux.Handle("POST /v1/hack/events/{slug}/judging/unlock", wrap(h.judgingLockRemoved))
	mux.Handle("GET /v1/hack/events/{slug}/judge/queue", wrap(h.judgeQueue))
	mux.Handle("GET /v1/hack/events/{slug}/judge/scores/{team}", wrap(h.getJudgeScores))
	mux.Handle("PUT /v1/hack/events/{slug}/judge/scores/{team}", wrap(h.putJudgeScores))
	mux.Handle("GET /v1/hack/events/{slug}/assignments", wrap(h.getManualAssignments))
	mux.Handle("PUT /v1/hack/events/{slug}/assignments", wrap(h.putManualAssignments))
	mux.Handle("GET /v1/hack/events/{slug}/judging/panels", wrap(h.getTrackPanels))
	mux.Handle("PUT /v1/hack/events/{slug}/judging/panels", wrap(h.putTrackPanels))
	mux.Handle("GET /v1/hack/events/{slug}/judging/preview", wrap(h.previewJudging))
}

type handler struct{ *core.Hack }
