package hack

import (
	"context"
	"sync"
	"time"

	"github.com/vsriram/simple-host/host/workers"
	"github.com/vsriram/simple-host/products/hack/app/events"
)

type contentDeliveryOwner struct {
	h *HackHandler
	*workers.Runner
	tracker *workers.Tracker
	once    sync.Once
}

func (h *HackHandler) ContentDeliveryOwner() workers.Owner {
	h.contentOnce.Do(func() {
		t := workers.NewTracker("hack.content_delivery")
		h.contentOwner = &contentDeliveryOwner{h: h, Runner: workers.NewRunner(t), tracker: t}
	})
	return h.contentOwner
}

func (o *contentDeliveryOwner) Start(ctx context.Context) {
	o.once.Do(func() {
		o.Go(ctx, func(ctx context.Context) {
			ticker := time.NewTicker(time.Minute)
			defer ticker.Stop()
			for {
				o.run(ctx)
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
			}
		})
	})
}

func (o *contentDeliveryOwner) pending(ctx context.Context) (int64, bool) {
	n, err := o.h.Events.PendingContentMail(ctx)
	if err != nil {
		o.tracker.Fail("count pending mail", err)
		return 0, false
	}
	o.tracker.SetPending(n)
	return n, true
}

func (o *contentDeliveryOwner) run(ctx context.Context) {
	o.tracker.Attempt()
	o.pending(ctx)
	// Only replace the mail dependency in this local handler view.
	h, mailer := o.h, o.h.Mailer
	if sender, ok := mailer.(interface {
		SendNotice(string, string, string) error
	}); ok {
		mailer = events.TrackContentSender(mailer, sender, o.tracker)
	}
	n, err := h.DeliverContentMail(ctx, mailer)
	o.record("deliver content mail", n, err)
	n, err = h.Events.QueueEntryReminders(ctx)
	o.record("queue entry reminders", n, err)
	n, err = h.Events.QueueJudgeReminders(ctx, h.Entries)
	o.record("queue judge reminders", n, err)
	n, err = h.Events.PruneNotices(ctx)
	o.record("prune notices", n, err)
	n, err = h.DeliverNoticeMail(ctx, mailer)
	o.record("deliver notice mail", n, err)
	o.pending(ctx)
}

func (o *contentDeliveryOwner) record(op string, n int64, err error) {
	if n > 0 {
		o.tracker.Progress()
	}
	if err != nil {
		o.tracker.Fail(op, err)
	}
}
