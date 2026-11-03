package hackhttp

import (
	"context"
	"github.com/vsriram/simple-host/internal/app/hack/events"
	"time"
)

func (h *HackHandler) queueEntryReminders(ctx context.Context) {
	h.eventsSvc().QueueEntryReminders(ctx)
}

func reminderIn(d time.Duration) string { return events.ReminderIn(d) }
