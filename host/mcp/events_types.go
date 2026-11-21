package mcp

import (
	"context"

	"github.com/vsriram/simple-host/host/data/notify"
)

// EventsBackend stores and removes MCP Events webhook subscriptions (the
// io.modelcontextprotocol/events extension, webhook mode). The handler
// package implements it; this package validates the request shape, the event
// name and the whsec_ secret format before calling it. Nil Config.Events
// means the server does not offer events.
type EventsBackend interface {
	// Subscribe creates or refreshes the subscription keyed by
	// (c.Owner, req.URL, req.Name, req.CanonicalArgs). It verifies the
	// callback endpoint (signed verification challenge) before a new key
	// becomes active. Errors that are not *EventError are internal errors.
	Subscribe(ctx context.Context, c Caller, req EventSubscribeRequest) (EventSubscribeResult, error)
	// Unsubscribe removes the subscription with that key. Removing one that
	// does not exist returns an *EventError with EventCodeNotFound.
	Unsubscribe(ctx context.Context, c Caller, req EventUnsubscribeRequest) error
}

// The subscription contract lives with the notifications it creates.
type (
	EventSubscribeRequest   = notify.EventSubscribeRequest
	EventSubscribeResult    = notify.EventSubscribeResult
	EventUnsubscribeRequest = notify.EventUnsubscribeRequest
	EventError              = notify.EventError
)

const (
	EventCodeNotFound          = notify.EventCodeNotFound
	EventCodeForbidden         = notify.EventCodeForbidden
	EventCodeResourceExhausted = notify.EventCodeResourceExhausted
	EventCodeUnsupported       = notify.EventCodeUnsupported
	EventCodeCallbackEndpoint  = notify.EventCodeCallbackEndpoint
)
