package papermetrics

import (
	"context"
	"net/http"
	"time"

	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/model"
)

// Recorder annotates measurements with facts resolved by the platform transport.
type Recorder interface {
	User(context.Context, *model.User)
	Site(*http.Request, string, string, *model.User)
	ResolveGroup(ctx context.Context, slug, stage, member string, published ...time.Time)
	Member(ctx context.Context, member string)
	ResolveSite(ctx context.Context, account, site, member string, write bool)
	WriteCommitted(ctx context.Context)
	Deploy(ctx context.Context)
	MutationResult(ctx context.Context, method, path string, status int)
}

type recorderKey struct{}

// With supplies the request recorder before dispatching to the transport.
func With(next http.Handler, recorder Recorder) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), recorderKey{}, recorder)))
	})
}

// From is the request's recorder, or one that records nothing.
func From(ctx context.Context) Recorder {
	if recorder, ok := ctx.Value(recorderKey{}).(Recorder); ok {
		return recorder
	}
	return quietRecorder{}
}

type quietRecorder struct{}

func (quietRecorder) User(context.Context, *model.User)                                  {}
func (quietRecorder) Site(*http.Request, string, string, *model.User)                    {}
func (quietRecorder) ResolveGroup(context.Context, string, string, string, ...time.Time) {}
func (quietRecorder) Member(context.Context, string)                                     {}
func (quietRecorder) ResolveSite(context.Context, string, string, string, bool)          {}
func (quietRecorder) WriteCommitted(context.Context)                                     {}
func (quietRecorder) Deploy(context.Context)                                             {}
func (quietRecorder) MutationResult(context.Context, string, string, int)                {}

// RecordSite records the site a request is for, on installs keeping paper
// metrics.
func RecordSite(ed *edition.Edition, r *http.Request, account, site string, user *model.User) {
	if ed.Identity().PaperMetrics {
		From(r.Context()).Site(r, account, site, user)
	}
}
