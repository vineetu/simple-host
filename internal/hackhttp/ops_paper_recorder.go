package hackhttp

import (
	"context"
	"net/http"
	"time"
)

// PaperRecorder annotates measurements with facts resolved by this transport.
type PaperRecorder interface {
	ResolveHackEvent(ctx context.Context, slug, stage, member string, published ...time.Time)
	HackMember(ctx context.Context, member string)
	RecordHackStage(ctx context.Context, eventID, stage string, origin ...time.Time)
	HackPhasesChanged(ctx context.Context)
}

type paperRecorderKey struct{}

// WithPaperRecorder supplies the request recorder before dispatching to the transport.
func WithPaperRecorder(next http.Handler, recorder PaperRecorder) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), paperRecorderKey{}, recorder)))
	})
}

func paperRecorder(ctx context.Context) PaperRecorder {
	if recorder, ok := ctx.Value(paperRecorderKey{}).(PaperRecorder); ok {
		return recorder
	}
	return quietPaperRecorder{}
}

type quietPaperRecorder struct{}

func (quietPaperRecorder) ResolveHackEvent(ctx context.Context, slug, stage, member string, published ...time.Time) {
}
func (quietPaperRecorder) HackMember(ctx context.Context, member string) {}
func (quietPaperRecorder) RecordHackStage(ctx context.Context, eventID, stage string, origin ...time.Time) {
}
func (quietPaperRecorder) HackPhasesChanged(ctx context.Context) {}
