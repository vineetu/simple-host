package handler

import (
	"context"
	"os"
	"path/filepath"
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

func TestSiteLoopDisabled(t *testing.T) {
	o := newSiteLoop("sites.disabled", func() bool { return false }, func(context.Context) { t.Fatal("disabled loop started") })
	o.Start(context.Background())
	o.Stop()
	o.Wait()
	s := o.Status()[0]
	if s.Enabled || s.Running || s.LastAttempt != nil || s.Pending != nil {
		t.Fatalf("unexpected disabled status: %+v", s)
	}
}

func TestSiteCertPendingCountsSharedIssuerDirectoryOnce(t *testing.T) {
	dir := t.TempDir()
	requests := filepath.Join(dir, "requests")
	if err := os.Mkdir(requests, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"olive", "cedar"} {
		if err := os.WriteFile(filepath.Join(requests, name), nil, 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(requests, "nested"), 0755); err != nil {
		t.Fatal(err)
	}
	h := &SiteHandler{siteDomain: "example.test", siteCertDir: dir, siteBase: "other.test", baseCertDir: dir, baseMove: baseMoveServe}
	h.updateSiteCertPending(context.Background())
	pending := h.siteLoop("sites.cert_requests").tracker.Snapshot().Pending
	if pending == nil || *pending != 2 {
		t.Fatalf("pending=%v; want two regular request files", pending)
	}
}
