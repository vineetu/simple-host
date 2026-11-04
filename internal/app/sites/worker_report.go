package sites

import (
	"context"
	"log"
)

// WorkerReport receives outcomes of a background pass without owning its lifecycle.
type WorkerReport interface {
	Progress()
	Fail(string, error)
}
type workerReportKey struct{}

func WithWorkerReport(ctx context.Context, report WorkerReport) context.Context {
	return context.WithValue(ctx, workerReportKey{}, report)
}
func ReportWorkerProgress(ctx context.Context) {
	if r, ok := ctx.Value(workerReportKey{}).(WorkerReport); ok {
		r.Progress()
	}
}
func ReportWorkerFailure(ctx context.Context, op string, err error) {
	if r, ok := ctx.Value(workerReportKey{}).(WorkerReport); ok {
		r.Fail(op, err)
	} else if err != nil {
		log.Printf("%s: %v", op, err)
	}
}
