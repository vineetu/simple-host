package accesstest

import (
	"context"

	"github.com/vsriram/simple-host/host/data/datahttp"
	notifyapp "github.com/vsriram/simple-host/host/data/notify"
	model "github.com/vsriram/simple-host/host/model"
)

// notificationBody is the envelope MCP or webhook subscriber n receives.
func notificationBody(h *datahttp.DataHandler, _ context.Context, n model.Notification, d model.NotificationDelivery, cursor int64) []byte {
	return notifyapp.WebhookBody(h.NotifyEnv(), h.Ed.Notifications().EventScoped, n, d, cursor, h.Limits.NotifyBodyMaxBytes)
}
