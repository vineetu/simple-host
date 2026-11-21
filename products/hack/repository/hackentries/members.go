package hackentries

import (
	"context"
	"database/sql"
	"errors"

	"github.com/vsriram/simple-host/host/sqlkit"
	"github.com/vsriram/simple-host/products/hack/repository/hackevents"
	"github.com/vsriram/simple-host/products/hack/repository/hackmembers"
)

// Who is in the event comes from hackmembers; these helpers turn its answers
// into the id lists the judging queries filter on (hackmembers never returns
// a nil list: a NULL array would make `NOT (x = ANY(...))` unknown).

// organiserJudges is the event's organiser_judges; none for a missing event.
func organiserJudges(ctx context.Context, q sqlkit.Querier, eventID string) ([]string, error) {
	ev, err := hackevents.GetEventByID(ctx, q, eventID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return ev.OrganiserJudges, err
}

// judgeIDs is the event's current judges (hackmembers.Judges).
func judgeIDs(ctx context.Context, q sqlkit.Querier, eventID string) ([]string, error) {
	oj, err := organiserJudges(ctx, q, eventID)
	if err != nil {
		return nil, err
	}
	return hackmembers.JudgeIDs(ctx, q, eventID, oj)
}

// isJudge is whether userID currently judges the event.
func isJudge(ctx context.Context, q sqlkit.Querier, eventID, userID string) (bool, error) {
	m, err := hackmembers.Member(ctx, q, eventID, userID, hackmembers.NoLock)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	oj, err := organiserJudges(ctx, q, eventID)
	return m.Judge(oj), err
}

// memberIDs is everyone in the event.
func memberIDs(ctx context.Context, q sqlkit.Querier, eventID string) ([]string, error) {
	return hackmembers.IDs(ctx, q, hackmembers.Filter{EventID: eventID})
}
