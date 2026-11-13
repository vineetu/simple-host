package hackhttp

import "context"

// queueEntryReminders runs on the content-mail ticker, before the notice sweep.
func (h *HackHandler) queueEntryReminders(ctx context.Context) {
	h.eventsSvc().QueueEntryReminders(ctx)
}
