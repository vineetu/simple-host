package handler

import (
	"context"
	"testing"
)

func TestImageScanEnqueueLifecycle(t *testing.T) {
	for _, mode := range []string{"before-start", "stop", "parent-cancel"} {
		t.Run(mode, func(t *testing.T) {
			s := NewImageScan(nil, nil, &fakeMatcher{}, nil, nil, ImageScanConfig{})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			release := make(chan struct{})
			// Release accepted work before cleanup joins, including on a failed assertion.
			released := false
			defer func() {
				if !released {
					close(release)
				}
				s.Stop()
				s.Wait()
			}()
			if mode != "before-start" {
				s.Start(ctx)
				s.Start(ctx)
			}
			entered := make(chan context.Context, 1)
			s.runEnqueue(func(ctx context.Context) { entered <- ctx; <-release })
			enqueueCtx := <-entered
			if mode == "parent-cancel" {
				cancel()
				s.runner.Wait() // The cancellation watcher closes admission, without joining enqueues.
			} else {
				s.Stop()
				s.Stop()
			}
			if err := enqueueCtx.Err(); err != nil {
				t.Fatalf("accepted enqueue canceled before drain: %v", err)
			}
			rejected := make(chan struct{}, 1)
			s.runEnqueue(func(context.Context) { rejected <- struct{}{} })
			s.enqueueMu.Lock()
			closing := s.closing
			s.enqueueMu.Unlock()
			if !closing {
				t.Fatal("enqueue admission still open after stop")
			}
			close(release)
			released = true
			s.Wait()
			select {
			case <-rejected:
				t.Error("enqueue admitted after stop")
			default:
			}
		})
	}
}
