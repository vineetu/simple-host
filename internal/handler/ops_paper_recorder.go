package handler

import (
	"context"
	"github.com/vsriram/simple-host/internal/model"
	"net/http"
	"time"
)

// PaperRecorder annotates measurements with facts resolved by this transport.
type PaperRecorder interface {
	User(context.Context, *model.User)
	Site(*http.Request, string, string, *model.User)
	ResolveGroup(ctx context.Context, slug, stage, member string, published ...time.Time)
	Member(ctx context.Context, member string)
	ResolveSite(ctx context.Context, account, site, member string, write bool)
	WriteCommitted(ctx context.Context)
	Deploy(ctx context.Context)
	MutationResult(ctx context.Context, method, path string, status int)
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

func (quietPaperRecorder) ResolveGroup(ctx context.Context, slug, stage, member string, published ...time.Time) {
}
func (quietPaperRecorder) Member(ctx context.Context, member string) {}
func (quietPaperRecorder) ResolveSite(ctx context.Context, account, site, member string, write bool) {
}
func (quietPaperRecorder) WriteCommitted(ctx context.Context)                                  {}
func (quietPaperRecorder) Deploy(ctx context.Context)                                          {}
func (quietPaperRecorder) MutationResult(ctx context.Context, method, path string, status int) {}

func (quietPaperRecorder) User(context.Context, *model.User)               {}
func (quietPaperRecorder) Site(*http.Request, string, string, *model.User) {}
func (h *SiteHandler) paperSite(r *http.Request, account, site string, user *model.User) {
	if h.ed.Identity().PaperMetrics {
		paperRecorder(r.Context()).Site(r, account, site, user)
	}
}
