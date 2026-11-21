package notify

import "time"

// The MCP Events subscription contract (events/subscribe and
// events/unsubscribe), owned here; host/mcp validates the wire shape.

// EventSubscribeRequest is a validated events/subscribe call.
type EventSubscribeRequest struct {
	// Name is one of the advertised event types: the record events
	// (record.added, record.changed, file.uploaded) everywhere, and on Simple
	// Host the site events too (site.deployed, domain.status_changed,
	// viewer.access_requested, visitor.signed_up).
	Name string
	// Site is arguments.site, the site's name as the site tools use it.
	Site string
	// Owner is arguments.owner: the handle of the account that holds the
	// site, when it is not the caller's own (a team they belong to, a site
	// they edit, a Simple Hack event). "" for the caller's own site.
	Owner string
	// Storage is arguments.storage, "" for every storage resource on the site.
	// Site events never carry one.
	Storage string
	// CanonicalArgs is the arguments object as canonical JSON (sorted keys,
	// no insignificant whitespace), part of the subscription key.
	CanonicalArgs string
	// URL is delivery.url, not yet checked for HTTPS or a public address.
	URL string
	// Secret is delivery.secret: "whsec_" + base64 of 24 to 64 bytes.
	Secret string
	// Cursor is the client's last persisted cursor, nil for "from now".
	Cursor *string
	// TTLSet is true when the request carried ttlMs. TTLSet with a nil TTL
	// asks for no expiry; !TTLSet asks for the server default.
	TTLSet bool
	TTL    *time.Duration
}

// EventSubscribeResult is the events/subscribe answer.
type EventSubscribeResult struct {
	ID            string
	RefreshBefore *time.Time // nil = no expiry
	Cursor        *string
	Truncated     bool
	// Refreshed is true when the key already existed; the response then
	// carries deliveryStatus {active, lastError}.
	Refreshed bool
	Active    bool
	LastError string // a category, never remote text
}

// EventUnsubscribeRequest is a validated events/unsubscribe call.
type EventUnsubscribeRequest struct {
	Name          string
	Site          string
	Storage       string
	CanonicalArgs string
	URL           string
}

// ChatGPT uses the earlier draft error numbers. A later MCP Events draft
// renumbered these to -32023 through -32027; keep the real client contract.
const (
	EventCodeNotFound          = -32011
	EventCodeForbidden         = -32012
	EventCodeResourceExhausted = -32013
	EventCodeUnsupported       = -32014
	EventCodeCallbackEndpoint  = -32015
)

// EventError is a protocol error from the backend, returned to the client as
// a JSON-RPC error with this code, message and data.
type EventError struct {
	Code    int
	Message string
	Data    map[string]any
}

func (e *EventError) Error() string { return e.Message }

// Subscriber is the connector caller an event subscription belongs to.
type Subscriber struct {
	Owner, GrantID, AppName string
	DeployOnly              bool
}

// SiteEventNames are the site events (Simple Host only).
var SiteEventNames = []string{"site.deployed", "domain.status_changed", "viewer.access_requested", "visitor.signed_up"}

// RecordEventNames fire on visitor writes to site storage.
var RecordEventNames = []string{"record.added", "record.changed", "file.uploaded"}
