package siteshttp

import (
	"context"
	"testing"
	"time"
)

func TestSiteLoopCancellationAndStatus(t *testing.T) {
	entered := make(chan struct{})
	exited := make(chan struct{})
	var o *siteLoop
	o = newSiteLoop("sites.test", func() bool { return true }, func(ctx context.Context) {
		o.periodic(ctx, 0, time.Hour, func(ctx context.Context) { close(entered); <-ctx.Done(); close(exited) })
	})
	t.Cleanup(func() { o.Stop(); o.Wait() })
	o.Start(context.Background())
	<-entered
	s := o.Status()[0]
	if !s.Enabled || !s.Running || s.LastAttempt == nil || s.LastProgress != nil || s.Pending != nil {
		t.Fatalf("unexpected active scan status: %+v", s)
	}
	o.Stop()
	o.Stop()
	o.Wait()
	select {
	case <-exited:
	default:
		t.Fatal("Wait did not join in-flight pass")
	}
	if o.Status()[0].Running {
		t.Fatal("joined loop still running")
	}
}
