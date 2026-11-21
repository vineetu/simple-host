package data

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	accounts "github.com/vsriram/simple-host/host/access"
	siterepo "github.com/vsriram/simple-host/host/sites/repo"
	ruleengine "github.com/vsriram/simple-host/host/storage/rules"
)

// BindingSchema is the merged declaration passed from startup to the service.
type BindingSchema = ruleengine.BindingSchema

// BindingCall is everything a provider may know about a request. It holds
// no header, query value or body: nothing a browser chose except which
// site it is on and which session it presented.
type BindingCall struct {
	SiteID   string // the site's id
	OwnerID  string // the account that holds the site
	SiteName string
	UserID   string // the signed-in visitor's account id; "" when anonymous
}

// BindingProvider is how the platform, and a product, supply trusted facts
// to row rules and answer the owner-on-site question. One provider is set
// per program at startup; nil means the platform provider alone.
type BindingProvider interface {
	// Schema returns the names and types this provider adds. It is read
	// once at startup. A product may add namespaces (for example event.*)
	// and new auth.* names. It may not declare a name the platform
	// declares, with one exception: it may declare auth.teams
	// (TypeTextList) to supply it. The namespace "row" is reserved.
	// A collision, a bad name or a bad type stops the program at startup.
	Schema() ruleengine.BindingSchema

	// Values returns a value for every name in needed that this provider
	// declares, read from the server's own data. It must return a value
	// for each such name: a text binding with nothing to say is
	// ruleengine.Null(), a bool is false, a list is empty. A site the
	// product knows nothing about (for example a site with no event)
	// gets exactly those empty values, not an error.
	// Any error means "cannot be read", and the caller denies (below).
	// Values must not cache across calls and must not read the request.
	Values(ctx context.Context, c BindingCall, needed []string) (ruleengine.Values, error)

	// OwnerOnSite reports whether userID counts as an owner of the site
	// for saved-data purposes. The platform's answer: userID is the
	// account that holds the site. Hack's answer: today's
	// HackSiteOwnerOnSite (host/repository/hackevents/
	// store_authority.go:9), unchanged: an approved participant of the
	// team that owns the site while the team may still change it
	// (event and team site not taken down, stage not judging, results or
	// archived, before the effective deadline), or an organiser of the
	// event while it is not taken down and not archived.
	// The request-shape conditions stay in the handler for every product
	// and are checked before this is called: not a visitor-AI credential,
	// not a framed page (X-SH-Framed), not a preview link, and the
	// request is on one of the site's own hosts
	// (storageSiteOwnerOnSite, host/handler/site_storage_access.go:206).
	OwnerOnSite(ctx context.Context, siteID, userID string) (bool, error)
}

// NoBindings gives a product type empty Schema and Values so it can
// implement OwnerOnSite first and add bindings later.
type NoBindings struct{}

func (NoBindings) Schema() ruleengine.BindingSchema { return nil }
func (NoBindings) Values(context.Context, BindingCall, []string) (ruleengine.Values, error) {
	return nil, nil
}

// PlatformSchema is the platform's own binding schema: auth.id,
// auth.signed_in, auth.role, auth.ai, auth.teams, and auth.checked when
// the spam check is configured. The platform's VALUES never come from a
// provider: the storage service computes them from the Call it already
// resolved (unexported platformValues, written in R3-S1a).
func PlatformSchema(spamCheckAvailable bool) ruleengine.BindingSchema {
	s := ruleengine.BindingSchema{
		"auth.id":        {Type: ruleengine.TypeText, Nullable: true},
		"auth.signed_in": {Type: ruleengine.TypeBool},
		"auth.role":      {Type: ruleengine.TypeText},
		"auth.ai":        {Type: ruleengine.TypeBool},
		"auth.teams":     {Type: ruleengine.TypeTextList},
	}
	if spamCheckAvailable {
		s["auth.checked"] = ruleengine.Binding{Type: ruleengine.TypeBool, AddCheckOnly: true}
	}
	return s
}

// DefaultProvider is used when no product sets one: no extra bindings,
// and OwnerOnSite is "userID may act as the owner of the site"
// (accounts.ActsAsOwner: the account itself, a member of the team that holds
// it, or an editor of the site).
type DefaultProvider struct {
	NoBindings
	DB *sql.DB
}

// bindings is the program's provider, the platform's own when none is set.
func (s *Service) bindings() BindingProvider {
	if s.deps.Bindings != nil {
		return s.deps.Bindings
	}
	return DefaultProvider{DB: s.db}
}

func (p DefaultProvider) OwnerOnSite(ctx context.Context, siteID, userID string) (bool, error) {
	if userID == "" {
		return false, nil
	}
	_, ownerID, name, err := siterepo.GetSiteOwner(ctx, p.DB, siteID)
	if err != nil {
		return false, err
	}
	return accounts.ActsAsOwner(ctx, p.DB, userID, ownerID, name)
}

// MergeBindingSchema validates the startup contract and copies the product's
// declarations into a fresh platform schema. The provider's Schema is read once.
func MergeBindingSchema(spamCheckAvailable bool, p BindingProvider) (ruleengine.BindingSchema, error) {
	s := PlatformSchema(spamCheckAvailable)
	if p == nil {
		return s, nil
	}
	extra := p.Schema()
	names := make([]string, 0, len(extra))
	for name := range extra {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		b := extra[name]
		parts := strings.Split(name, ".")
		if len(parts) != 2 || !bindingIdentifier(parts[0]) || !bindingIdentifier(parts[1]) || reservedBindingNamespace(parts[0]) {
			return nil, fmt.Errorf("storage binding %q: invalid or reserved name", name)
		}
		if b.Type < ruleengine.TypeBool || b.Type > ruleengine.TypeTextList || b.Nullable && b.Type != ruleengine.TypeText && b.Type != ruleengine.TypeNumber {
			return nil, fmt.Errorf("storage binding %q: invalid type", name)
		}
		// auth.checked remains platform-owned even when it is unavailable.
		if _, exists := PlatformSchema(true)[name]; exists {
			if name != "auth.teams" || b != (ruleengine.Binding{Type: ruleengine.TypeTextList}) {
				return nil, fmt.Errorf("storage binding %q: collides with platform schema", name)
			}
		}
		s[name] = b
	}
	return s, nil
}

func bindingIdentifier(s string) bool {
	if len(s) == 0 || len(s) > 64 {
		return false
	}
	for i, c := range []byte(s) {
		if c != '_' && !(c >= 'a' && c <= 'z') && !(c >= 'A' && c <= 'Z') && !(i > 0 && c >= '0' && c <= '9') {
			return false
		}
	}
	// A namespace must also be usable in the grammar; fields can be keywords.
	return true
}

func reservedBindingNamespace(s string) bool {
	switch s {
	case "true", "false", "null", "in", "contains", "is", "not", "lower", "row":
		return true
	}
	return false
}

// SnapshotBindings keeps the provider declaration fixed after startup. Values
// and owner checks still call the live provider on each authorization stage.
func SnapshotBindings(p BindingProvider) BindingProvider {
	if p == nil {
		return nil
	}
	schema := ruleengine.BindingSchema{}
	for name, binding := range p.Schema() {
		schema[name] = binding
	}
	return declaredBindings{BindingProvider: p, schema: schema}
}

type declaredBindings struct {
	BindingProvider
	schema ruleengine.BindingSchema
}

func (p declaredBindings) Schema() ruleengine.BindingSchema { return p.schema }
