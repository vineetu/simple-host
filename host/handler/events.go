package handler

import (
	"context"

	"github.com/vsriram/simple-host/host/data/datahttp"
	notifications "github.com/vsriram/simple-host/host/data/notify"
	"github.com/vsriram/simple-host/host/mcp"
)

// EventsBackend is MCP Events' backend: Data's notification engine with the
// handler's current keys. It lives with the composition so the Data routes
// never import the MCP server.
func EventsBackend(h *datahttp.DataHandler) mcp.EventsBackend { return eventsBackend{h} }

type eventsBackend struct{ h *datahttp.DataHandler }

func (b eventsBackend) EventsAvailability() error {
	return notifications.EventsAvailability(b.h.NotifyEnv())
}

func (b eventsBackend) Subscribe(ctx context.Context, c mcp.Caller, req mcp.EventSubscribeRequest) (mcp.EventSubscribeResult, error) {
	return b.h.Notifier().Subscribe(ctx, b.h.NotifyEnv(), eventSubscriber(c), req)
}

func (b eventsBackend) Unsubscribe(ctx context.Context, c mcp.Caller, req mcp.EventUnsubscribeRequest) error {
	return b.h.Notifier().Unsubscribe(ctx, eventSubscriber(c), req)
}

func eventSubscriber(c mcp.Caller) notifications.Subscriber {
	return notifications.Subscriber{Owner: c.Owner, GrantID: c.OAuthGrantID, AppName: c.AppName, DeployOnly: c.DeployOnly}
}
