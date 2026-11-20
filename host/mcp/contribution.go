package mcp

// Contribution is everything a product adds to the tool server. The zero value adds nothing.
type Contribution struct {
	Families      []FamilyTools
	Notifications []Tool
	SiteVariants  map[string]func(Tool, Limits) Tool
	Instructions  func(mode string) string
	Rewrite       func(text, apiHost string) string
	StorageError  func(message string) string
	Apps          func(AppContext) AppContribution
	// MutationNames answers a call to a mutation the product used to offer
	// under that name and no longer does, by tool name. The call is refused
	// with the message, which ends in its code in square brackets.
	MutationNames map[string]RetiredMutation
}

// RetiredMutation is the refusal for one retired tool name.
type RetiredMutation struct {
	Message string // what the caller reads, ending "[<Code>]"
	Code    string // the refusal code recorded for a call through run_tool
}

// NewServer builds the tool server. A nil or zero contribution builds only the
// platform's tools.
func NewServer(cfg Config) *Server { return newServer(cfg) }

// Inventory returns the composed documentation inventory with Events enabled.
func Inventory(product Contribution) []InventoryTool {
	limits := DefaultLimits()
	cfg := Config{Limits: &limits, Events: inventoryEvents{}, Contribution: &product, Teams: true}
	var apps AppContribution
	if product.Apps != nil {
		apps = product.Apps(AppContext{})
	}
	return inventoryTools(cfg, apps.AppFamily)
}

// AppContext contains server facts and shared metadata, with no request data.
type AppContext struct {
	APIHost string
	Meta    func(AppScreen) map[string]any
}

// AppContribution supplies one server's product screens.
type AppContribution struct {
	Screens   func(Caller) []AppScreen
	Resources func(Caller, bool) []map[string]any
	Gate      func(Caller, bool) bool
	Read      func(Request, Caller, bool) (Response, bool)
	// AppFamily is one more family, built from the tools of
	// Contribution.Families and registered after them. Nil adds none.
	AppFamily func(base []Tool) FamilyTools
	// SitesPage is what the platform's own screens carry when they open on
	// one of the product's connections: a script placed ahead of the screen's
	// own, and the product's name. Nil keeps the platform's page.
	SitesPage func() (script, product string)
}

// FamilyTools groups declarations in listing order.
type FamilyTools = familyTools

func (s *Server) productScreens(c Caller) []AppScreen {
	if screens := s.apps.Screens; screens != nil {
		return screens(c)
	}
	return nil
}
func (s *Server) productAppsGate(c Caller, ui bool) bool {
	return s.apps.Gate != nil && s.apps.Gate(c, ui)
}
func (s *Server) productAppResources(c Caller, allowed bool) []any {
	var out []any
	if resources := s.apps.Resources; resources != nil {
		for _, resource := range resources(c, allowed) {
			out = append(out, resource)
		}
	}
	return out
}
