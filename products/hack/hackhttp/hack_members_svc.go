package hackhttp

import (
	"context"

	"github.com/vsriram/simple-host/products/hack/app/members"
	"github.com/vsriram/simple-host/products/hack/model"
)

func (h *HackHandler) membersSvc() *members.Service {
	return members.New(h.database, h.archiveEventTx, h.deleteEventTx, func(ctx context.Context, ev model.Event) {
		paperRecorder(ctx).RecordHackStage(ctx, ev.ID, ev.Stage, ev.UpdatedAt)
		paperRecorder(ctx).HackPhasesChanged(ctx)
		h.pinDue(ctx, ev.ID)
		// requestEventCert is a no-op for archived events, as in setStage.
	}, h.removeEventFiles, h.removeOrphanTeamSites)
}
